package dtc

import (
	"strings"
	"testing"
)

func TestIndexedLookup(t *testing.T) {
	ix, err := NewIndexed([]byte("# header\n\nP0301,Cylinder 1 Misfire Detected\nP0420,Catalyst\nU0100,Lost Comms\n"), "test")
	if err != nil {
		t.Fatalf("NewIndexed: %v", err)
	}
	if ix.Len() != 3 {
		t.Fatalf("indexed %d records, want 3", ix.Len())
	}

	for _, tc := range []struct{ code, want string }{
		{"P0301", "Cylinder 1 Misfire Detected"},
		{"p0420", "Catalyst"},
		{"U0100", "Lost Comms"},
	} {
		def, ok := ix.Lookup(tc.code)
		if !ok || def.Description != tc.want {
			t.Errorf("Lookup(%q) = %+v, %v; want %q", tc.code, def, ok, tc.want)
		}
	}

	// Misses must be misses, including either side of the range and a gap
	// in the middle, which is where a binary search goes wrong.
	for _, code := range []string{"P0000", "P0350", "Z9999", "U9999"} {
		if def, ok := ix.Lookup(code); ok {
			t.Errorf("Lookup(%q) = %+v, want miss", code, def)
		}
	}
}

// An unsorted file would make every lookup unreliable, so it must be rejected
// at load rather than producing wrong answers later.
func TestIndexedRejectsUnsorted(t *testing.T) {
	if _, err := NewIndexed([]byte("P0420,Catalyst\nP0301,Misfire\n"), "test"); err == nil {
		t.Error("NewIndexed accepted unsorted input")
	}
}

// Indexed and the map loader must agree, or which one is used changes results.
func TestIndexedMatchesTable(t *testing.T) {
	raw, err := embedded.ReadFile("data/generic.csv")
	if err != nil {
		t.Fatal(err)
	}

	table, err := LoadCSV(strings.NewReader(string(raw)), "generic")
	if err != nil {
		t.Fatal(err)
	}
	ix, err := NewIndexed(raw, "generic")
	if err != nil {
		t.Fatal(err)
	}

	if ix.Len() != len(table) {
		t.Fatalf("indexed %d records, table has %d", ix.Len(), len(table))
	}
	for code, want := range table {
		got, ok := ix.Lookup(code)
		if !ok || got.Description != want.Description {
			t.Errorf("Lookup(%q) = %+v, want %q", code, got, want.Description)
		}
	}
}
