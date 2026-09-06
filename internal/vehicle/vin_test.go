package vehicle

import "testing"

func TestParseVIN(t *testing.T) {
	tests := []struct {
		name  string
		vin   string
		make_ string
		year  int
	}{
		{"honda accord", "1HGCM82633A004352", "honda", 2003},
		{"bmw 3 series", "WBADT43452G296302", "bmw", 2002},
		{"volkswagen", "3VWFE21C04M000001", "volkswagen", 2004},
		// Position 7 is alphabetic, so the year letter reads as 2010 or
		// later rather than thirty years earlier.
		{"tesla, unknown WMI", "5YJ3E1EA7JF000316", "", 2018},
		// Position 7 numeric, so the same letter reads as the earlier cycle.
		{"acura legend", "JH4KA7660MC003887", "acura", 1991},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ParseVIN(tc.vin)
			if err != nil {
				t.Fatalf("ParseVIN(%q): %v", tc.vin, err)
			}
			if v.Make != tc.make_ {
				t.Errorf("make = %q, want %q", v.Make, tc.make_)
			}
			if v.Year != tc.year {
				t.Errorf("year = %d, want %d", v.Year, tc.year)
			}
			if v.WMI != tc.vin[:3] {
				t.Errorf("wmi = %q, want %q", v.WMI, tc.vin[:3])
			}
		})
	}
}

func TestParseVINNormalises(t *testing.T) {
	v, err := ParseVIN("  wba dt43452g296302 ")
	if err != nil {
		t.Fatalf("ParseVIN: %v", err)
	}
	if v.Raw != "WBADT43452G296302" {
		t.Errorf("raw = %q", v.Raw)
	}
}

func TestParseVINRejectsBadInput(t *testing.T) {
	for _, in := range []string{
		"",
		"TOOSHORT",
		"1HGCM82633A00435212345",
		"1HGCM82633A0043I2", // I is not a legal VIN character
		"1HGCM82633A0043O2", // nor is O
		"1HGCM82633A0043Q2", // nor is Q
	} {
		if v, err := ParseVIN(in); err == nil {
			t.Errorf("ParseVIN(%q) = %+v, want error", in, v)
		}
	}
}

// The check digit is the only self-verification a VIN carries, so a corrupted
// read should be visible rather than silently accepted.
func TestCheckDigit(t *testing.T) {
	valid, err := ParseVIN("WBADT43452G296302")
	if err != nil {
		t.Fatal(err)
	}
	if !valid.CheckDigitValid {
		t.Error("known-good VIN reported an invalid check digit")
	}

	// Same VIN with position 9 altered.
	corrupt, err := ParseVIN("WBADT43451G296302")
	if err != nil {
		t.Fatal(err)
	}
	if corrupt.CheckDigitValid {
		t.Error("corrupted VIN reported a valid check digit")
	}
}

// The year letter cycle repeats every thirty years; position 7 is what tells
// 1980-2009 from 2010 onward.
func TestModelYearDisambiguation(t *testing.T) {
	for _, tc := range []struct {
		vin  string
		year int
	}{
		{"WBADT43452G296302", 2002}, // letter cycle, numeric position 7
		{"5YJ3E1EA7JF000316", 2018}, // same letter, alphabetic position 7
	} {
		v, err := ParseVIN(tc.vin)
		if err != nil {
			t.Fatalf("ParseVIN(%q): %v", tc.vin, err)
		}
		if v.Year != tc.year {
			t.Errorf("%s year = %d, want %d", tc.vin, v.Year, tc.year)
		}
	}
}

func TestMakeForWMI(t *testing.T) {
	for wmi, want := range map[string]string{
		"1HG": "honda", "wba": "bmw", "JTH": "lexus", "ZZZ": "",
	} {
		if got := MakeForWMI(wmi); got != want {
			t.Errorf("MakeForWMI(%q) = %q, want %q", wmi, got, want)
		}
	}
}
