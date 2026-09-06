package dtc

import (
	"path/filepath"
	"strings"
	"testing"
)

// The embedded files ship in the binary, so a typo in one is a release bug.
// Parsing them under test is the cheapest guard.
func TestBuiltinCatalogParses(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}

	if n := len(c.(Layered)); n != 3 {
		t.Fatalf("builtin has %d layers, want 3", n)
	}

	for _, code := range []string{"P0301", "P0420", "U0100", "C1A00"} {
		def, ok := c.Lookup(code)
		if !ok {
			t.Errorf("Lookup(%q) missing", code)
			continue
		}
		if def.Description == "" {
			t.Errorf("Lookup(%q) has empty description", code)
		}
	}
}

// Each code must resolve from the layer that legitimately owns it, or the
// provenance shown next to a description is a lie.
func TestBuiltinLayerProvenance(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}

	for _, tc := range []struct{ code, source string }{
		{"P0301", "generic"}, // ISO/SAE range
		{"P1106", "vendor"},  // manufacturer range
		{"C1A00", "curated"}, // hand-maintained, absent from the datasets
	} {
		def, ok := c.Lookup(tc.code)
		if !ok {
			t.Errorf("Lookup(%q) missing", tc.code)
			continue
		}
		if def.Source != tc.source {
			t.Errorf("%s source = %q, want %q", tc.code, def.Source, tc.source)
		}
	}
}

// The import writes real data; a smoke test on its size catches a run that
// silently produced almost nothing.
func TestBuiltinCatalogIsPopulated(t *testing.T) {
	for _, tc := range []struct {
		path string
		min  int
	}{
		{"data/generic.csv", 5000},
		{"data/manufacturer.csv", 5000},
	} {
		raw, err := embedded.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		ix, err := NewIndexed(raw, "test")
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if ix.Len() < tc.min {
			t.Errorf("%s has %d records, want at least %d", tc.path, ix.Len(), tc.min)
		}
	}
}

// P1106 is the case that forced the make column: several marques define it
// incompatibly, so answering without knowing the make would send someone to
// the wrong part.
func TestResolverDisambiguatesByMake(t *testing.T) {
	r, err := NewResolver("")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	code, err := Parse("P1106")
	if err != nil {
		t.Fatal(err)
	}

	acura := r.WithMake("acura").Describe(code)
	if !strings.Contains(acura.Description, "BARO") {
		t.Errorf("acura P1106 = %q, want the BARO definition", acura.Description)
	}
	if acura.Make != "acura" {
		t.Errorf("acura P1106 make = %q", acura.Make)
	}

	chevy := r.WithMake("chevy").Describe(code)
	if !strings.Contains(chevy.Description, "MAP") {
		t.Errorf("chevy P1106 = %q, want the MAP definition", chevy.Description)
	}

	if acura.Description == chevy.Description {
		t.Error("acura and chevy resolved P1106 identically")
	}

	// With no make, say the code is ambiguous rather than picking one.
	unknown := r.Describe(code)
	if !strings.Contains(unknown.Description, "--make") {
		t.Errorf("unattributed P1106 = %q, want a request for the make", unknown.Description)
	}
}

// A code only one make defines needs no disambiguation, and must not be
// reported as ambiguous just because it lives in the vendor file.
func TestResolverUnambiguousVendorCode(t *testing.T) {
	r, err := NewResolver("")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	code, err := Parse("C1A15")
	if err != nil {
		t.Fatal(err)
	}
	def := r.Describe(code)
	if strings.Contains(def.Description, "--make") {
		t.Errorf("C1A15 = %q, want a plain definition", def.Description)
	}
}

func TestLoadCSV(t *testing.T) {
	in := `# a comment

P0301,Cylinder 1 Misfire Detected
p0420 , Catalyst System Efficiency Below Threshold, Bank 1
`
	table, err := LoadCSV(strings.NewReader(in), "test")
	if err != nil {
		t.Fatalf("LoadCSV: %v", err)
	}
	if len(table) != 2 {
		t.Fatalf("loaded %d entries, want 2", len(table))
	}

	// Lowercase input must normalise, and a description containing a comma
	// must survive intact.
	def, ok := table.Lookup("P0420")
	if !ok {
		t.Fatal("P0420 missing")
	}
	if want := "Catalyst System Efficiency Below Threshold, Bank 1"; def.Description != want {
		t.Errorf("description = %q, want %q", def.Description, want)
	}
}

func TestLoadCSVRejectsBadCode(t *testing.T) {
	if _, err := LoadCSV(strings.NewReader("X9999,nope\n"), "test"); err == nil {
		t.Error("LoadCSV accepted an invalid code")
	}
	if _, err := LoadCSV(strings.NewReader("P0301 no comma\n"), "test"); err == nil {
		t.Error("LoadCSV accepted a line with no comma")
	}
}

// The user layer is optional; a missing file must not break startup.
func TestLoadFileMissingIsEmpty(t *testing.T) {
	table, err := LoadFile(filepath.Join(t.TempDir(), "absent.csv"), "user")
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(table) != 0 {
		t.Errorf("loaded %d entries from a missing file", len(table))
	}
}

func TestLayeredPrefersEarlierLayer(t *testing.T) {
	l := Layered{
		Table{"P0301": {Code: "P0301", Description: "user override", Source: "user"}},
		Table{"P0301": {Code: "P0301", Description: "builtin", Source: "builtin"}},
	}

	def, ok := l.Lookup("P0301")
	if !ok || def.Description != "user override" {
		t.Errorf("Lookup = %+v, want the user override", def)
	}
}

func TestResolverFallsBackToEncoding(t *testing.T) {
	r, err := NewResolver("")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	known, _ := Parse("P0301")
	if def := r.Describe(known); def.Description != "Cylinder 1 Misfire Detected" {
		t.Errorf("known code = %q, want the catalog entry", def.Description)
	}

	// P1FFF is vendor space and in no catalog, but the encoding still proves
	// the system and that no shared definition can exist.
	unknown, _ := Parse("P1FFF")
	def := r.Describe(unknown)
	if !strings.Contains(def.Description, "manufacturer-specific") {
		t.Errorf("unknown code = %q, want the derived fallback", def.Description)
	}
	if def.Source != "derived from encoding" {
		t.Errorf("source = %q, want the derived marker", def.Source)
	}
}
