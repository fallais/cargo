package dtc

import (
	"bufio"
	"embed"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

//go:embed data/*.csv
var embedded embed.FS

// Provenance labels. Every Definition carries one, and the UI shows it, so a
// hand-checked entry is distinguishable on screen from a scraped one.
const (
	SourceCurated = "curated"
	SourceGeneric = "generic"
	SourceVendor  = "vendor"
	// SourceVendorUnattributed marks a vendor definition that names no
	// marque. It is the same row, said with less confidence.
	SourceVendorUnattributed = "vendor, no make"
	SourceDerived            = "derived from encoding"
)

// Definition is a human-readable meaning for a code, plus where that meaning
// came from. Provenance is not decoration: the open catalogs differ in
// licensing and in how much they can be trusted, so anything we display should
// be able to say which layer it came from.
type Definition struct {
	Code        string
	Description string
	Source      string
	// Make is set for manufacturer codes, which mean different things on
	// different vehicles: P1106 is a barometric fault on Acura and a MAP
	// fault on GM. A definition without its make attached is a guess.
	Make string
}

// Catalog maps codes to definitions.
type Catalog interface {
	Lookup(code string) (Definition, bool)
}

// MultiCatalog is implemented by catalogs that can hold several definitions
// for one code, which manufacturer catalogs must: the same number is reused
// across makes with unrelated meanings.
type MultiCatalog interface {
	Catalog
	LookupAll(code string) []Definition
}

// Table is an in-memory Catalog.
type Table map[string]Definition

func (t Table) Lookup(code string) (Definition, bool) {
	d, ok := t[strings.ToUpper(code)]
	return d, ok
}

func (t Table) LookupAll(code string) []Definition {
	if d, ok := t.Lookup(code); ok {
		return []Definition{d}
	}
	return nil
}

// Layered queries catalogs in order and takes the first hit, so a
// vehicle-specific or user-supplied file can override a shared one.
type Layered []Catalog

func (l Layered) Lookup(code string) (Definition, bool) {
	for _, c := range l {
		if c == nil {
			continue
		}
		if d, ok := c.Lookup(code); ok {
			return d, true
		}
	}
	return Definition{}, false
}

// LookupAll returns every definition from the first layer that has any.
func (l Layered) LookupAll(code string) []Definition {
	for _, c := range l {
		if c == nil {
			continue
		}
		if m, ok := c.(MultiCatalog); ok {
			if defs := m.LookupAll(code); len(defs) > 0 {
				return defs
			}
			continue
		}
		if d, ok := c.Lookup(code); ok {
			return []Definition{d}
		}
	}
	return nil
}

// LoadCSV reads "code,description" records. Blank lines and lines starting
// with '#' are skipped so the data files can carry attribution headers.
//
// Descriptions may themselves contain commas, so the line is split only on the
// first one rather than being run through encoding/csv with its quoting rules.
func LoadCSV(r io.Reader, source string) (Table, error) {
	table := make(Table)
	scanner := bufio.NewScanner(r)

	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		code, description, found := strings.Cut(text, ",")
		if !found {
			return nil, fmt.Errorf("dtc: %s line %d: missing comma", source, line)
		}

		code = strings.ToUpper(strings.TrimSpace(code))
		if _, err := Parse(code); err != nil {
			return nil, fmt.Errorf("dtc: %s line %d: %w", source, line, err)
		}

		table[code] = Definition{
			Code:        code,
			Description: strings.TrimSpace(description),
			Source:      source,
		}
	}

	return table, scanner.Err()
}

// LoadFile reads a catalog from disk. A missing file is not an error: the
// user-supplied layer is optional by design.
func LoadFile(path, source string) (Table, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Table{}, nil
		}
		return nil, err
	}
	defer f.Close()

	return LoadCSV(f, source)
}

var (
	builtinOnce sync.Once
	builtin     Layered
	builtinErr  error
)

// Builtin is the catalog compiled into the binary, in precedence order:
// hand-curated entries, then codes in the ISO/SAE-controlled ranges, then
// manufacturer codes.
//
// Keeping this embedded rather than fetched is deliberate. The tool is used in
// a garage on a laptop with no connectivity, and a diagnostic that only works
// online is not a diagnostic.
func Builtin() (Catalog, error) {
	builtinOnce.Do(func() {
		// The descriptions come from community datasets; it is the range
		// rules in J2012 that decide which file a code lands in. So the
		// label says "generic", not "SAE": claiming the standard as the
		// source of the wording would overstate it.
		for _, f := range []struct{ path, source string }{
			{"data/local.csv", SourceCurated},
			{"data/generic.csv", SourceGeneric},
			{"data/manufacturer.csv", SourceVendor},
		} {
			b, err := embedded.ReadFile(f.path)
			if err != nil {
				builtinErr = err
				return
			}
			// Indexed rather than LoadCSV: the embedded files are
			// sorted, so they can be searched in place instead of
			// being expanded into a map on every run.
			ix, err := NewIndexed(b, f.source)
			if err != nil {
				builtinErr = err
				return
			}
			builtin = append(builtin, ix)
		}
	})

	return builtin, builtinErr
}

// Resolver attaches descriptions to decoded codes.
type Resolver struct {
	Catalog Catalog
	// Make narrows manufacturer codes to one marque. Without it a code
	// that several makes define differently cannot be resolved honestly,
	// and Describe says so rather than picking one.
	Make string
}

// NewResolver builds a resolver over the embedded catalog, with an optional
// user file layered on top so owners can add codes for their own vehicle.
func NewResolver(userPath string) (*Resolver, error) {
	base, err := Builtin()
	if err != nil {
		return nil, err
	}

	layers := Layered{}
	if userPath != "" {
		user, err := LoadFile(userPath, "user")
		if err != nil {
			return nil, err
		}
		layers = append(layers, user)
	}

	return &Resolver{Catalog: append(layers, base)}, nil
}

// WithMake returns a resolver narrowed to one marque.
func (r *Resolver) WithMake(make string) *Resolver {
	out := *r
	out.Make = strings.ToLower(strings.TrimSpace(make))
	return &out
}

// Describe returns the best available meaning for a code. It never fails: a
// code with no catalog entry falls back to what the encoding itself proves.
func (r *Resolver) Describe(d DTC) Definition {
	if r == nil || r.Catalog == nil {
		return r.derive(d)
	}

	defs := candidates(r.Catalog, d.Code)
	if len(defs) == 0 {
		return r.derive(d)
	}

	// Prefer the marque we are attached to, whatever else the code means.
	if r.Make != "" {
		for _, def := range defs {
			if def.Description != "" && strings.EqualFold(def.Make, r.Make) {
				return def
			}
		}
	}

	// Nothing here was written for this vehicle. A vendor definition
	// compiled from another marque's list is not an answer about this car:
	// "Door Lock Cylinder Circuit Open" for what a Dacia means by B1560
	// sends someone to the wrong part with full confidence. The derived
	// description says less and is true.
	//
	// The curated layer is exempt. Its entries carry no make because they
	// were checked by hand and meant to apply broadly, not because their
	// provenance was lost.
	if r.Make != "" && allScrapedVendor(defs) {
		return r.derive(d)
	}

	if len(defs) == 1 {
		if defs[0].Description == "" {
			return r.derive(d)
		}
		return unattributed(defs[0])
	}

	// Definitions can agree even when several makes list the code, in
	// which case there is nothing to disambiguate.
	if same := singleDescription(defs); same != "" {
		out := defs[0]
		out.Description, out.Make = same, ""
		return out
	}

	// Otherwise say that plainly instead of picking one at random and
	// sending someone to replace the wrong part.
	out := r.derive(d)
	out.Description = fmt.Sprintf("%s, defined differently by %d makes; pass --make to choose",
		d.System, len(defs))
	return out
}

// allScrapedVendor reports whether every candidate came from the scraped
// vendor layer.
//
// Two layers are exempt. A generic code is ISO/SAE-controlled and means the
// same on every vehicle, so the marque is irrelevant to it. A curated entry
// carries no make because it was checked by hand and meant to apply broadly,
// not because its provenance was lost.
func allScrapedVendor(defs []Definition) bool {
	for _, def := range defs {
		if def.Source != SourceVendor {
			return false
		}
	}
	return len(defs) > 0
}

// unattributed marks a scraped vendor definition that names no marque, so the
// display can show that it is a hint rather than this vehicle's answer. Around
// a third of the imported vendor rows arrive with no make, because the pooled
// upstream files do not record one.
func unattributed(def Definition) Definition {
	if def.Source == SourceVendor && def.Make == "" {
		def.Source = SourceVendorUnattributed
	}
	return def
}

// candidates returns every definition a catalog holds for a code.
func candidates(c Catalog, code string) []Definition {
	if m, ok := c.(MultiCatalog); ok {
		return m.LookupAll(code)
	}
	if def, ok := c.Lookup(code); ok {
		return []Definition{def}
	}
	return nil
}

// singleDescription returns the shared description if every candidate agrees.
func singleDescription(defs []Definition) string {
	first := defs[0].Description
	for _, d := range defs[1:] {
		if !strings.EqualFold(d.Description, first) {
			return ""
		}
	}
	return first
}

func (r *Resolver) derive(d DTC) Definition {
	return Definition{
		Code:        d.Code,
		Description: d.Describe(),
		Source:      SourceDerived,
	}
}

// Makes returns every marque the built-in catalog can distinguish.
func Makes() ([]string, error) {
	c, err := Builtin()
	if err != nil {
		return nil, err
	}

	layered, ok := c.(Layered)
	if !ok {
		return nil, nil
	}

	seen := make(map[string]struct{})
	var makes []string
	for _, layer := range layered {
		ix, ok := layer.(*Indexed)
		if !ok {
			continue
		}
		for _, m := range ix.Makes() {
			if _, dup := seen[m]; dup {
				continue
			}
			seen[m] = struct{}{}
			makes = append(makes, m)
		}
	}

	sort.Strings(makes)
	return makes, nil
}
