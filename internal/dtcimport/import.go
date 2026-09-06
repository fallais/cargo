package dtcimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/pkg/log"

	"go.uber.org/zap"
)

// Options control an import run.
type Options struct {
	// OutDir receives generic.csv and manufacturer.csv.
	OutDir string
	// CacheDir keeps downloaded files so a re-run is offline and so the
	// diff of an import can be attributed to a known input.
	CacheDir string
	// Offline uses only what is already cached.
	Offline bool
	// Timeout bounds a single download.
	Timeout time.Duration
}

func (o *Options) setDefaults() {
	if o.OutDir == "" {
		o.OutDir = filepath.Join("internal", "dtc", "data")
	}
	if o.CacheDir == "" {
		o.CacheDir = filepath.Join(os.TempDir(), "cargo-dtc-cache")
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
}

// Result reports what an import produced.
type Result struct {
	Generic      int
	Manufacturer int
	Makes        int
	Sources      int
	Skipped      int
	Conflicts    int
	Redundant    int
	Licences     []Licence
}

// Run fetches every source, classifies the records and writes the catalog.
func Run(ctx context.Context, opts Options) (*Result, error) {
	opts.setDefaults()

	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return nil, err
	}

	var (
		result   = &Result{}
		generic  = map[string]Record{}
		specific = map[string]Record{} // keyed by code + "\x00" + make
		licences = map[string]Licence{}
	)

	for _, source := range Sources() {
		data, err := fetch(ctx, source, opts)
		if err != nil {
			// One unreachable file should not sink an import that can
			// still produce a usable catalog from the rest.
			log.Warn("Skipping source", zap.String("source", source.Name), zap.Error(err))
			result.Skipped++
			continue
		}

		records, err := source.Parse(data)
		if err != nil {
			log.Warn("Skipping unparseable source",
				zap.String("source", source.Name), zap.Error(err))
			result.Skipped++
			continue
		}

		result.Sources++
		licences[source.Licence.URL] = source.Licence

		for _, r := range records {
			decoded, err := dtc.Parse(r.Code)
			if err != nil {
				continue
			}
			r.Make = source.Make
			r.Source = source.Name

			// The code's own encoding decides which file it belongs in,
			// not which file it arrived in: the pooled p_codes.txt holds
			// both ISO/SAE and vendor codes.
			if decoded.Kind == dtc.Generic {
				if existing, ok := generic[r.Code]; ok {
					if !strings.EqualFold(existing.Description, r.Description) {
						result.Conflicts++
					}
					continue // first source wins
				}
				generic[r.Code] = r
				continue
			}

			key := r.Code + "\x00" + r.Make
			if existing, ok := specific[key]; ok {
				if !strings.EqualFold(existing.Description, r.Description) {
					result.Conflicts++
				}
				continue
			}
			specific[key] = r
		}
	}

	if result.Sources == 0 {
		return nil, fmt.Errorf("no sources could be read")
	}

	// A make-specific row saying exactly what the unattributed row says
	// carries no information and just inflates the committed file.
	for key, r := range specific {
		if r.Make == "" {
			continue
		}
		if base, ok := specific[r.Code+"\x00"]; ok &&
			strings.EqualFold(base.Description, r.Description) {
			delete(specific, key)
			result.Redundant++
		}
	}

	if err := writeGeneric(filepath.Join(opts.OutDir, "generic.csv"), generic); err != nil {
		return nil, err
	}
	if err := writeManufacturer(filepath.Join(opts.OutDir, "manufacturer.csv"), specific); err != nil {
		return nil, err
	}

	makes := map[string]struct{}{}
	for _, r := range specific {
		if r.Make != "" {
			makes[r.Make] = struct{}{}
		}
	}

	result.Generic = len(generic)
	result.Manufacturer = len(specific)
	result.Makes = len(makes)
	for _, l := range licences {
		result.Licences = append(result.Licences, l)
	}
	sort.Slice(result.Licences, func(i, j int) bool {
		return result.Licences[i].URL < result.Licences[j].URL
	})

	return result, nil
}

// fetch returns a source's bytes, from the cache when possible.
func fetch(ctx context.Context, source Source, opts Options) ([]byte, error) {
	sum := sha256.Sum256([]byte(source.URL))
	path := filepath.Join(opts.CacheDir, hex.EncodeToString(sum[:8])+".txt")

	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return data, nil
	}
	if opts.Offline {
		return nil, fmt.Errorf("not cached and running offline")
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cargo-dtc-importer")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Warn("Could not cache source", zap.String("path", path), zap.Error(err))
	}
	return data, nil
}

const genericHeader = `# ISO/SAE-controlled (generic) trouble codes: the same meaning on every vehicle.
#
# Generated by "cargo import". Do not edit by hand.
# Records are sorted by code; internal/dtc verifies that on load.
#
# format: code,description
`

const manufacturerHeader = `# Manufacturer-specific trouble codes.
#
# Generated by "cargo import". Do not edit by hand.
#
# J2012 reserves these ranges but not their meanings, so the same code means
# different things on different marques. Rows carry the make where the source
# named one; an empty make is a definition the source did not attribute, and is
# used only when nothing better matches.
#
# Records are sorted by code then make; internal/dtc verifies that on load.
#
# format: code,make,description
`

func writeGeneric(path string, records map[string]Record) error {
	codes := make([]string, 0, len(records))
	for code := range records {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	var b strings.Builder
	b.WriteString(genericHeader)
	for _, code := range codes {
		fmt.Fprintf(&b, "%s,%s\n", code, records[code].Description)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeManufacturer(path string, records map[string]Record) error {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	// The key is code + NUL + make, and NUL sorts below every printable
	// byte, so this yields exactly the code-then-make order the loader
	// requires, with unattributed rows first within each code.
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(manufacturerHeader)
	for _, key := range keys {
		r := records[key]
		fmt.Fprintf(&b, "%s,%s,%s\n", r.Code, r.Make, r.Description)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
