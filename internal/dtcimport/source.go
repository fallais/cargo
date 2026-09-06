// Package dtcimport builds the embedded trouble-code catalog from the open
// datasets.
//
// The catalog is committed, not fetched at runtime, so this runs as a
// maintenance command and its output is reviewed as a diff like any other
// change. That is the whole reason the data files are CSV: an import that
// quietly changed 3,000 definitions should be visible before it is merged.
package dtcimport

import (
	"fmt"
	"regexp"
	"strings"

	"cargo/internal/dtc"
)

// Licence records how a dataset may be used. It is reported at the end of an
// import and mirrored in the NOTICE file, so adding a source without settling
// its terms is hard to do by accident.
type Licence struct {
	SPDX   string
	Holder string
	URL    string
}

// Source is one dataset to import.
type Source struct {
	Name string
	// URL is the file to fetch.
	URL string
	// Make attributes every record in this file to one marque. Empty means
	// the file pools codes from many, which is how the per-letter files are
	// organised.
	Make string
	// Parse turns the file's bytes into records.
	Parse func(data []byte) ([]Record, error)
	// Licence covers this file's contents.
	Licence Licence
}

// Record is one imported definition before classification.
type Record struct {
	Code        string
	Make        string
	Description string
	Source      string
}

var (
	mitWal33D = Licence{
		SPDX:   "MIT",
		Holder: "Wal33D (Waleed Judah)",
		URL:    "https://github.com/Wal33D/dtc-database",
	}
	mitDtcdb = Licence{
		SPDX:   "MIT",
		Holder: "trobbins",
		URL:    "https://github.com/todrobbins/dtcdb",
	}
)

const wal33dRaw = "https://raw.githubusercontent.com/Wal33D/dtc-database/main/data/source-data/"

// pooledFiles hold codes from many manufacturers, grouped by code letter.
// Records from these carry no make, because the source does not say.
var pooledFiles = []string{
	"p_codes.txt",
	"b_codes.txt",
	"c_codes.txt",
	"u_codes.txt",
	"other_codes.txt",
}

// makeFiles are attributed to a single marque by their filename. This is what
// makes the ~700 cross-make conflicts resolvable: P1106 means one thing on an
// Acura and another on a Chevrolet, and only the make tells them apart.
var makeFiles = []string{
	"acura", "audi", "bmw", "buick", "cadillac", "chevy", "chrysler", "dodge",
	"ford", "geo", "gm", "gmc", "honda", "infiniti", "jaguar", "jeep", "kia",
	"lexus", "lincoln", "mazda", "mercedes", "mercury", "mitsubishi", "nissan",
	"oldsmobile", "plymouth", "pontiac", "saturn", "subaru", "suzuki", "toyota",
	"volkswagen",
}

// Sources returns every dataset the importer knows about.
func Sources() []Source {
	sources := make([]Source, 0, len(pooledFiles)+len(makeFiles)+1)

	for _, file := range pooledFiles {
		sources = append(sources, Source{
			Name:    "wal33d/" + file,
			URL:     wal33dRaw + file,
			Parse:   ParseDashList,
			Licence: mitWal33D,
		})
	}

	for _, make := range makeFiles {
		sources = append(sources, Source{
			Name:    "wal33d/" + make,
			URL:     fmt.Sprintf("%s%s_codes.txt", wal33dRaw, make),
			Make:    make,
			Parse:   ParseDashList,
			Licence: mitWal33D,
		})
	}

	return append(sources, Source{
		Name:    "dtcdb/generic",
		URL:     "https://raw.githubusercontent.com/todrobbins/dtcdb/master/generic.csv",
		Parse:   ParseCSV,
		Licence: mitDtcdb,
	})
}

// dashRecord matches "P0301 - Cylinder 1 Misfire Detected", allowing the
// hyphen, en dash, em dash or colon that the sources mix.
var dashRecord = regexp.MustCompile(`^([PpCcBbUu][0-3][0-9A-Fa-f]{3})\s*[-–—:]\s*(\S.*)$`)

// ParseDashList reads the "CODE - Description" format.
//
// Lines that do not match are section headings and prose, which these files
// carry between the records. They are skipped rather than reported: a parser
// that fails on a heading would make the import unusable.
func ParseDashList(data []byte) ([]Record, error) {
	var records []Record

	for _, line := range strings.Split(string(data), "\n") {
		m := dashRecord.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		records = append(records, Record{
			Code:        strings.ToUpper(m[1]),
			Description: cleanDescription(m[2]),
		})
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("no records found")
	}
	return records, nil
}

// ParseCSV reads "code,description", skipping a header row if present.
func ParseCSV(data []byte) ([]Record, error) {
	var records []Record

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		code, description, found := strings.Cut(line, ",")
		if !found {
			continue
		}
		code = strings.ToUpper(strings.TrimSpace(code))
		if _, err := dtc.Parse(code); err != nil {
			continue // header row, or a section label
		}

		records = append(records, Record{
			Code:        code,
			Description: cleanDescription(description),
		})
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("no records found")
	}
	return records, nil
}

// dashes normalises the en and em dashes the sources use mid-description down
// to a plain hyphen, so the catalog stays ASCII and two datasets writing the
// same definition with different dashes compare equal.
var dashes = strings.NewReplacer("\u2013", "-", "\u2014", "-")

// cleanDescription normalises whitespace and dashes, and strips the trailing
// punctuation and stray quoting the sources carry, so the same definition
// arriving from two datasets compares equal instead of duplicating.
func cleanDescription(s string) string {
	s = dashes.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, `"'`)
	return strings.TrimRight(s, ".;, ")
}
