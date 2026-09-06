package dtc

import "testing"

func TestDecode(t *testing.T) {
	tests := []struct {
		name   string
		a, b   byte
		code   string
		system System
		kind   Kind
	}{
		// The previous decoder derived digit 1 from the whole first byte
		// rather than its high nibble, so every code whose second nibble had
		// its low two bits set came out as a manufacturer code. These four
		// are the regression cases.
		{"generic O2 sensor", 0x01, 0x33, "P0133", Powertrain, Generic},
		{"cylinder 1 misfire", 0x03, 0x01, "P0301", Powertrain, Generic},
		{"lost comms with ECM", 0xC1, 0x00, "U0100", Network, Generic},
		{"body code", 0x81, 0x23, "B0123", Body, Generic},

		{"catalyst efficiency", 0x04, 0x20, "P0420", Powertrain, Generic},
		{"all zero", 0x00, 0x00, "P0000", Powertrain, Generic},
		{"all ones", 0xFF, 0xFF, "U3FFF", Network, Reserved},

		{"powertrain vendor range", 0x11, 0x34, "P1134", Powertrain, Manufacturer},
		{"powertrain P2 is generic", 0x21, 0x87, "P2187", Powertrain, Generic},
		{"powertrain P30xx is vendor", 0x30, 0x11, "P3011", Powertrain, Manufacturer},
		{"powertrain P34xx is generic", 0x34, 0x11, "P3411", Powertrain, Generic},

		{"chassis generic", 0x40, 0x35, "C0035", Chassis, Generic},
		{"chassis vendor", 0x5A, 0x00, "C1A00", Chassis, Manufacturer},
		{"chassis C2 stays vendor", 0x62, 0x10, "C2210", Chassis, Manufacturer},
		{"chassis C3 reserved", 0x70, 0x00, "C3000", Chassis, Reserved},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Decode(tc.a, tc.b)
			if got.Code != tc.code {
				t.Errorf("Decode(%#02x, %#02x) code = %q, want %q", tc.a, tc.b, got.Code, tc.code)
			}
			if got.System != tc.system {
				t.Errorf("Decode(%#02x, %#02x) system = %v, want %v", tc.a, tc.b, got.System, tc.system)
			}
			if got.Kind != tc.kind {
				t.Errorf("Decode(%#02x, %#02x) kind = %v, want %v", tc.a, tc.b, got.Kind, tc.kind)
			}
			if want := uint16(tc.a)<<8 | uint16(tc.b); got.Raw != want {
				t.Errorf("Decode(%#02x, %#02x) raw = %#04x, want %#04x", tc.a, tc.b, got.Raw, want)
			}
		})
	}
}

// Parse and Decode must agree, so a code read off the wire and the same code
// typed into a catalog file resolve to the same entry.
func TestParseRoundTripsDecode(t *testing.T) {
	for a := 0; a <= 0xFF; a++ {
		for b := 0; b <= 0xFF; b += 17 { // stride keeps the test quick
			want := Decode(byte(a), byte(b))
			got, err := Parse(want.Code)
			if err != nil {
				t.Fatalf("Parse(%q): %v", want.Code, err)
			}
			if got != want {
				t.Errorf("Parse(%q) = %+v, want %+v", want.Code, got, want)
			}
		}
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	for _, in := range []string{"", "P030", "P03011", "X0301", "P03G1", "PABCD"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %+v, want error", in, got)
		}
	}
}

func TestParseAcceptsLowercaseAndSpace(t *testing.T) {
	got, err := Parse("  p0301 ")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Code != "P0301" {
		t.Errorf("Parse = %q, want P0301", got.Code)
	}
}

// A code with no catalog entry must still say something true.
func TestDescribeNeverEmpty(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"P1134", "Powertrain, manufacturer-specific (consult service documentation)"},
		{"P0301", "Powertrain, generic"},
		{"C3000", "Chassis, reserved range"},
	} {
		d, err := Parse(tc.code)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.code, err)
		}
		if got := d.Describe(); got != tc.want {
			t.Errorf("%s.Describe() = %q, want %q", tc.code, got, tc.want)
		}
	}
}
