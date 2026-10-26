package serial

import (
	"errors"
	"testing"
)

func TestParseAck(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		service  byte
		want     error
	}{
		{"mode 04 acknowledged", "44", obdClear, nil},
		{"UDS clear acknowledged", "54", clearDiagnosticInformation, nil},
		{"pending, then acknowledged", "7F1478\r54", clearDiagnosticInformation, nil},
		{"engine running", "7F0422", obdClear, ErrUDSConditions},
		{"needs a session", "7F147F", clearDiagnosticInformation, ErrUDSWrongSession},
		{"KWP needs a session", "7F1480", clearDiagnosticInformation, ErrUDSWrongSession},
		{"not supported", "7F1411", clearDiagnosticInformation, ErrUDSNotSupported},
		{"silence", "NO DATA", obdClear, ErrNoData},
		{"someone else's reply", "5902FF", clearDiagnosticInformation, ErrParse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := parseAck(tc.response, tc.service)
			if tc.want == nil {
				if err != nil {
					t.Errorf("parseAck(%q) = %v, want success", tc.response, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("parseAck(%q) = %v, want %v", tc.response, err, tc.want)
			}
		})
	}
}

// A reply of nothing but "still working" is not an acknowledgement.
func TestParseAckOnlyPending(t *testing.T) {
	if err := parseAck("7F1478", clearDiagnosticInformation); err == nil {
		t.Error("a bare pending reply was taken as a cleared module")
	}
}

func TestClearRequests(t *testing.T) {
	if udsClearAll != "14FFFFFF" {
		t.Errorf("UDS clear = %s, want 14FFFFFF", udsClearAll)
	}
	if kwpClearAll != "14FF00" {
		t.Errorf("KWP2000 clear = %s, want 14FF00", kwpClearAll)
	}
}
