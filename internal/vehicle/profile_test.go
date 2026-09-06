package vehicle

import (
	"path/filepath"
	"testing"
)

// Never having configured a vehicle is the normal starting state, not an error.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	g, err := Load(filepath.Join(t.TempDir(), "garage.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(g.Vehicles) != 0 {
		t.Errorf("loaded %d vehicles from a missing file", len(g.Vehicles))
	}
}

func TestSaveAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garage.json")

	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	g.Upsert(Profile{Name: "Daily", Make: "ford", Year: 2008, VIN: "1FAHP35N58W123456"})
	g.SetActive(0)
	if err := g.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	active, ok := reloaded.Active()
	if !ok {
		t.Fatal("no active vehicle after reload")
	}
	if active.Make != "ford" || active.Year != 2008 {
		t.Errorf("active = %+v, want the ford", active)
	}
}

// A VIN read from the car must not wipe details the user typed in: the VIN
// gives us the marque and year but says nothing about the model.
func TestUpsertKeepsUserEnteredFields(t *testing.T) {
	g := &Garage{}
	g.Upsert(Profile{Name: "Daily", Make: "ford", Model: "Focus", VIN: "1FAHP35N58W123456"})

	detected, err := ParseVIN("1FAHP35N58W123456")
	if err != nil {
		t.Fatal(err)
	}
	g.Upsert(FromVIN(detected))

	if len(g.Vehicles) != 1 {
		t.Fatalf("upsert created %d vehicles, want 1", len(g.Vehicles))
	}
	v := g.Vehicles[0]
	if v.Model != "Focus" {
		t.Errorf("model = %q, want the user's value to survive", v.Model)
	}
	if v.Name != "Daily" {
		t.Errorf("name = %q, want the user's value to survive", v.Name)
	}
	if v.Year != 2008 {
		t.Errorf("year = %d, want the detected value to apply", v.Year)
	}
}

// Two cars in a household is exactly the case the make column exists for, so
// distinct VINs must stay distinct.
func TestUpsertDistinguishesVehicles(t *testing.T) {
	g := &Garage{}
	g.Upsert(Profile{Make: "ford", VIN: "1FAHP35N58W123456"})
	g.Upsert(Profile{Make: "honda", VIN: "1HGCM82633A004352"})

	if len(g.Vehicles) != 2 {
		t.Fatalf("got %d vehicles, want 2", len(g.Vehicles))
	}
}

func TestActivePrefersExplicitSelection(t *testing.T) {
	g := &Garage{}
	g.Upsert(Profile{Make: "ford", VIN: "1FAHP35N58W123456"})
	g.Upsert(Profile{Make: "honda", VIN: "1HGCM82633A004352"})

	// With more than one vehicle and nothing selected, refuse to guess.
	if _, ok := g.Active(); ok {
		t.Error("Active picked a vehicle with no selection made")
	}

	g.SetActive(1)
	active, ok := g.Active()
	if !ok || active.Make != "honda" {
		t.Errorf("active = %+v, want the honda", active)
	}
}

// A single vehicle needs no selecting.
func TestActiveWithOneVehicle(t *testing.T) {
	g := &Garage{}
	g.Upsert(Profile{Make: "ford", VIN: "1FAHP35N58W123456"})

	if active, ok := g.Active(); !ok || active.Make != "ford" {
		t.Errorf("active = %+v, %v; want the only vehicle", active, ok)
	}
}

func TestRemoveClearsSelection(t *testing.T) {
	g := &Garage{}
	g.Upsert(Profile{Make: "ford", VIN: "1FAHP35N58W123456"})
	g.Upsert(Profile{Make: "honda", VIN: "1HGCM82633A004352"})
	g.SetActive(1)

	g.Remove(1)
	if len(g.Vehicles) != 1 {
		t.Fatalf("got %d vehicles, want 1", len(g.Vehicles))
	}
	if g.ActiveVIN != "" {
		t.Errorf("ActiveVIN = %q, want it cleared", g.ActiveVIN)
	}
}

func TestLabel(t *testing.T) {
	for _, tc := range []struct {
		profile Profile
		want    string
	}{
		{Profile{Name: "Daily"}, "Daily"},
		{Profile{Make: "ford", Year: 2008}, "2008 Ford"},
		{Profile{Make: "ford", Model: "Focus"}, "Ford Focus"},
		{Profile{}, "Unnamed vehicle"},
	} {
		if got := tc.profile.Label(); got != tc.want {
			t.Errorf("Label(%+v) = %q, want %q", tc.profile, got, tc.want)
		}
	}
}
