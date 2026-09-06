package dtcimport

import "testing"

func TestParseDashList(t *testing.T) {
	in := []byte(`DTC Codes - P0100-P0199 - Fuel and Air Metering

P0100 - Mass or Volume Air Flow Circuit Malfunction
P0101 – Mass Air Flow Circuit Range/Performance
P0102: Mass Air Flow Circuit Low Input
p0103 - mass air flow circuit high input.
not a code at all
U0100 - Lost Communication With ECM/PCM   "A"
`)

	records, err := ParseDashList(in)
	if err != nil {
		t.Fatalf("ParseDashList: %v", err)
	}
	if len(records) != 5 {
		t.Fatalf("parsed %d records, want 5: %+v", len(records), records)
	}

	// Codes normalise to upper case whatever the source used.
	if records[3].Code != "P0103" {
		t.Errorf("code = %q, want P0103", records[3].Code)
	}
	// Trailing punctuation is stripped so the same definition arriving from
	// two datasets compares equal instead of duplicating.
	if got, want := records[3].Description, "mass air flow circuit high input"; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	// Runs of whitespace collapse, and stray quoting is removed.
	if got, want := records[4].Description, `Lost Communication With ECM/PCM "A`; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// Section headings must not be mistaken for records, and a file of nothing but
// prose is an error rather than a silent empty import.
func TestParseDashListRejectsEmpty(t *testing.T) {
	if _, err := ParseDashList([]byte("just a heading\n\nand prose\n")); err == nil {
		t.Error("ParseDashList accepted a file with no records")
	}
}

func TestParseCSV(t *testing.T) {
	in := []byte("dtc, description\n\nDTC Codes - P0100-P0199\nP0100,Mass or Volume Air Flow Circuit Malfunction\nP0301,Cylinder 1 Misfire Detected\n")

	records, err := ParseCSV(in)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("parsed %d records, want 2: %+v", len(records), records)
	}
	if records[1].Code != "P0301" {
		t.Errorf("code = %q, want P0301", records[1].Code)
	}
}

// Every source must declare a licence, or CREDITS.md cannot be trusted.
func TestEverySourceHasLicence(t *testing.T) {
	sources := Sources()
	if len(sources) < 30 {
		t.Fatalf("only %d sources configured", len(sources))
	}

	seen := map[string]bool{}
	for _, s := range sources {
		if s.Licence.SPDX == "" || s.Licence.Holder == "" || s.Licence.URL == "" {
			t.Errorf("source %q has an incomplete licence: %+v", s.Name, s.Licence)
		}
		if s.Parse == nil {
			t.Errorf("source %q has no parser", s.Name)
		}
		if seen[s.Name] {
			t.Errorf("duplicate source name %q", s.Name)
		}
		seen[s.Name] = true
	}
}
