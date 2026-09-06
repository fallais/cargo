package serial

import (
	"errors"
	"testing"

	"cargo/internal/dtc"
)

// The adapter's spacing changes with ATS0 and its framing changes when a reply
// spans several CAN frames. Every shape below is the same data.
func TestNormalizeHexAcceptsEveryFraming(t *testing.T) {
	want := "430133"
	for _, in := range []string{
		"43 01 33",
		"430133",
		"43 01 33\r\n",
		"43\r01\r33",
	} {
		got, err := normalizeHex(in)
		if err != nil {
			t.Errorf("normalizeHex(%q): %v", in, err)
			continue
		}
		if h := hexString(got); h != want {
			t.Errorf("normalizeHex(%q) = %s, want %s", in, h, want)
		}
	}
}

func TestNormalizeHexMultiFrame(t *testing.T) {
	in := "014\r0: 43 03 01 33 01\r1: 71 02 34 00 00\r"
	got, err := normalizeHex(in)
	if err != nil {
		t.Fatalf("normalizeHex: %v", err)
	}
	if h, want := hexString(got), "43030133017102340000"; h != want {
		t.Errorf("normalizeHex = %s, want %s", h, want)
	}
}

// Regression: with ATS0 in the init sequence these replies arrive unspaced,
// and the previous whitespace-splitting parsers failed on every one of them.
func TestParsePIDsWithAndWithoutSpaces(t *testing.T) {
	tests := []struct {
		name    string
		spaced  string
		unspace string
		parse   func(string) (int, error)
		want    int
	}{
		{"rpm", "41 0C 1A F8", "410C1AF8", ParseRPM, 1726},
		{"speed", "41 0D 40", "410D40", ParseSpeed, 64},
		{"distance", "41 31 01 2C", "4131012C", ParseDistance, 300},
		{
			"coolant", "41 05 5A", "41055A",
			func(s string) (int, error) { v, err := ParseTemp(s, 0x05); return int(v), err },
			50,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, in := range []string{tc.spaced, tc.unspace} {
				got, err := tc.parse(in)
				if err != nil {
					t.Errorf("parse(%q): %v", in, err)
					continue
				}
				if got != tc.want {
					t.Errorf("parse(%q) = %d, want %d", in, got, tc.want)
				}
			}
		})
	}
}

// A reply for a different PID must be rejected, not silently misread as the
// one we asked for.
func TestParsePIDRejectsMismatchedPID(t *testing.T) {
	if _, err := ParseRPM("41 05 5A"); !errors.Is(err, ErrParse) {
		t.Errorf("ParseRPM on a coolant reply: err = %v, want ErrParse", err)
	}
}

func TestParseDTCs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		// CAN: a count byte follows the mode echo, so the payload is odd.
		{"can single", "43 01 01 33", []string{"P0133"}},
		{"can pair", "43 02 01 33 01 71", []string{"P0133", "P0171"}},
		{"can padded", "43 02 01 33 01 71 00 00", []string{"P0133", "P0171"}},
		{"can none", "43 00", nil},
		{"can unspaced", "430201330171", []string{"P0133", "P0171"}},

		// Legacy buses send three fixed slots and no count, leaving the
		// payload even.
		{"legacy three", "43 01 33 01 71 02 34", []string{"P0133", "P0171", "P0234"}},
		{"legacy padded", "43 01 33 00 00 00 00", []string{"P0133"}},

		// Multi-frame, which is how more than two codes actually arrive.
		{"multiframe", "014\r0: 43 03 01 33 01\r1: 71 02 34 00 00", []string{"P0133", "P0171", "P0234"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDTCs(tc.in, 0x03, dtc.Stored)
			if err != nil {
				t.Fatalf("ParseDTCs(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ParseDTCs(%q) = %v, want %v", tc.in, codes(got), tc.want)
			}
			for i, code := range tc.want {
				if got[i].Code != code {
					t.Errorf("code %d = %q, want %q", i, got[i].Code, code)
				}
				if got[i].Status != dtc.Stored {
					t.Errorf("code %d status = %v, want stored", i, got[i].Status)
				}
			}
		})
	}
}

func TestParseDTCsCarriesStatus(t *testing.T) {
	got, err := ParseDTCs("47 01 01 33", 0x07, dtc.Pending)
	if err != nil {
		t.Fatalf("ParseDTCs: %v", err)
	}
	if len(got) != 1 || got[0].Status != dtc.Pending {
		t.Errorf("got %+v, want one pending code", got)
	}
}

// Status text must be caught before hex parsing: "NO DATA" is itself valid hex
// and used to decode into a phantom trouble code.
func TestStatusRepliesBecomeSentinels(t *testing.T) {
	tests := []struct {
		in   string
		want error
	}{
		{"NO DATA", ErrNoData},
		{"UNABLE TO CONNECT", ErrUnableToConnect},
		{"BUS INIT: ERROR", ErrBusInit},
		{"CAN ERROR", ErrCANError},
		{"STOPPED", ErrStopped},
		{"?", ErrBadCommand},
		{"BUFFER FULL", ErrBufferFull},
		{"SEARCHING...", ErrNoData},
	}

	for _, tc := range tests {
		if _, err := ParseDTCs(tc.in, 0x03, dtc.Stored); !errors.Is(err, tc.want) {
			t.Errorf("ParseDTCs(%q) err = %v, want %v", tc.in, err, tc.want)
		}
		if _, err := ParseRPM(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("ParseRPM(%q) err = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func TestParseVoltage(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
	}{
		{"12.5V", 12.5}, {"12.5", 12.5}, {" 13.8v ", 13.8},
	} {
		got, err := ParseVoltage(tc.in)
		if err != nil {
			t.Errorf("ParseVoltage(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseVoltage(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	if _, err := ParseVoltage("nope"); !errors.Is(err, ErrParse) {
		t.Error("ParseVoltage accepted junk")
	}
}

func hexString(b []byte) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0F])
	}
	return string(out)
}

func codes(d []dtc.DTC) []string {
	out := make([]string, len(d))
	for i := range d {
		out[i] = d[i].Code
	}
	return out
}
