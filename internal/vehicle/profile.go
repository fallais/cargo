package vehicle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Profile is one vehicle the user has told us about.
type Profile struct {
	// Name is what the user calls it, so a garage of several cars is
	// readable at a glance.
	Name  string `json:"name"`
	Make  string `json:"make"`
	Model string `json:"model,omitempty"`
	Year  int    `json:"year,omitempty"`
	VIN   string `json:"vin,omitempty"`
	// Detected records whether this came from the car rather than the user,
	// so an automatic guess never silently overwrites a deliberate choice.
	Detected bool      `json:"detected,omitempty"`
	LastSeen time.Time `json:"last_seen,omitempty"`
}

// Label renders a profile for a list.
func (p Profile) Label() string {
	if p.Name != "" {
		return p.Name
	}

	parts := make([]string, 0, 3)
	if p.Year > 0 {
		parts = append(parts, fmt.Sprint(p.Year))
	}
	if p.Make != "" {
		parts = append(parts, titleCase(p.Make))
	}
	if p.Model != "" {
		parts = append(parts, p.Model)
	}
	if len(parts) == 0 {
		return "Unnamed vehicle"
	}
	return strings.Join(parts, " ")
}

// titleCase capitalises a marque for display. The names are ASCII and single
// words, so this does not need the Unicode-aware casing rules.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// FromVIN builds a profile from a decoded VIN.
func FromVIN(v VIN) Profile {
	return Profile{
		Make:     v.Make,
		Year:     v.Year,
		VIN:      v.Raw,
		Detected: true,
		LastSeen: time.Now(),
	}
}

// Garage is the set of vehicles the user has configured, with one active.
//
// Keeping several is worth the small extra state: a household with two cars
// would otherwise have the make silently wrong every other session, which is
// exactly the failure the make column exists to prevent.
type Garage struct {
	Vehicles []Profile `json:"vehicles"`
	// ActiveVIN identifies the selected vehicle. VIN is used rather than an
	// index so the file stays meaningful if it is hand-edited.
	ActiveVIN  string `json:"active_vin,omitempty"`
	ActiveName string `json:"active_name,omitempty"`

	path string
}

// GaragePath is where the garage is stored.
func GaragePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "cargo", "garage.json")
}

// Load reads the garage. A missing file yields an empty garage, not an error:
// having never configured a vehicle is the normal starting state.
func Load(path string) (*Garage, error) {
	if path == "" {
		return &Garage{}, nil
	}

	g := &Garage{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return g, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, g); err != nil {
		return nil, fmt.Errorf("garage %s: %w", path, err)
	}
	g.path = path
	return g, nil
}

// Save writes the garage back.
func (g *Garage) Save() error {
	if g.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(g.path, append(data, '\n'), 0o644)
}

// Active returns the selected vehicle.
func (g *Garage) Active() (Profile, bool) {
	for _, v := range g.Vehicles {
		if g.ActiveVIN != "" && v.VIN == g.ActiveVIN {
			return v, true
		}
	}
	for _, v := range g.Vehicles {
		if g.ActiveName != "" && v.Name == g.ActiveName {
			return v, true
		}
	}
	// A single vehicle needs no selecting.
	if len(g.Vehicles) == 1 {
		return g.Vehicles[0], true
	}
	return Profile{}, false
}

// SetActive selects a vehicle by its position in the list.
func (g *Garage) SetActive(index int) {
	if index < 0 || index >= len(g.Vehicles) {
		return
	}
	g.ActiveVIN = g.Vehicles[index].VIN
	g.ActiveName = g.Vehicles[index].Name
}

// Upsert adds a vehicle, or updates the matching one.
//
// A detected profile never overwrites a field the user set by hand: the VIN
// tells us the marque reliably but nothing about the model, and clobbering a
// typed-in model with an empty string would be a poor trade.
func (g *Garage) Upsert(p Profile) int {
	for i, existing := range g.Vehicles {
		if !sameVehicle(existing, p) {
			continue
		}

		merged := existing
		if p.Make != "" {
			merged.Make = p.Make
		}
		if p.Model != "" {
			merged.Model = p.Model
		}
		if p.Year > 0 {
			merged.Year = p.Year
		}
		if p.VIN != "" {
			merged.VIN = p.VIN
		}
		if p.Name != "" {
			merged.Name = p.Name
		}
		if !p.LastSeen.IsZero() {
			merged.LastSeen = p.LastSeen
		}
		g.Vehicles[i] = merged
		return i
	}

	g.Vehicles = append(g.Vehicles, p)
	return len(g.Vehicles) - 1
}

// Remove deletes a vehicle by position, clearing the selection if it was the
// one removed.
func (g *Garage) Remove(index int) {
	if index < 0 || index >= len(g.Vehicles) {
		return
	}

	removed := g.Vehicles[index]
	g.Vehicles = append(g.Vehicles[:index], g.Vehicles[index+1:]...)

	if g.ActiveVIN != "" && g.ActiveVIN == removed.VIN {
		g.ActiveVIN = ""
	}
	if g.ActiveName != "" && g.ActiveName == removed.Name {
		g.ActiveName = ""
	}
}

// sameVehicle decides whether two profiles describe one car. A VIN is
// definitive; without one, fall back to the user's own name for it.
func sameVehicle(a, b Profile) bool {
	if a.VIN != "" && b.VIN != "" {
		return a.VIN == b.VIN
	}
	if a.Name != "" && b.Name != "" {
		return strings.EqualFold(a.Name, b.Name)
	}
	return false
}

// Makes returns the marques the trouble-code catalog can distinguish, so the
// picker offers what actually changes an answer rather than a list of every
// carmaker.
func Makes(catalogMakes []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(catalogMakes))

	for _, m := range catalogMakes {
		m = strings.ToLower(strings.TrimSpace(m))
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}

	sort.Strings(out)
	return out
}
