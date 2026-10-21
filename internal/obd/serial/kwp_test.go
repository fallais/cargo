package serial

import (
	"errors"
	"testing"
)

// The Sandero power steering module, in a diagnostic session, with nothing
// stored.
func TestParseKWPDTCsNone(t *testing.T) {
	codes, err := ParseKWPDTCs("5700")
	if err != nil {
		t.Fatalf("ParseKWPDTCs: %v", err)
	}
	if len(codes) != 0 {
		t.Errorf("got %v, want no codes", codes)
	}
}

func TestParseKWPDTCsRecords(t *testing.T) {
	// Two codes, then a pending reply ahead of the answer, as UDS modules
	// also send.
	codes, err := ParseKWPDTCs("7F1778" + "5702" + "5A6020" + "012360")
	if err != nil {
		t.Fatalf("ParseKWPDTCs: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("got %d codes, want 2: %v", len(codes), codes)
	}
	if codes[0].FullCode() != "C1A60" || codes[1].FullCode() != "P0123" {
		t.Errorf("got %s and %s, want C1A60 and P0123", codes[0].FullCode(), codes[1].FullCode())
	}
	// The status byte is Renault's own, so it must not be read as UDS bits.
	if codes[0].StatusMask != 0 || codes[0].WarningActive() || codes[0].FailingNow() {
		t.Errorf("KWP status was interpreted as UDS: %+v", codes[0])
	}
}

// Outside a diagnostic session the module answers NRC 80, which has to read as
// "try a session", not as a module that does not do this.
func TestParseKWPDTCsWrongSession(t *testing.T) {
	if _, err := ParseKWPDTCs("7F1780"); !errors.Is(err, ErrUDSWrongSession) {
		t.Errorf("err = %v, want ErrUDSWrongSession", err)
	}
	if _, err := ParseKWPDTCs("7F1711"); !errors.Is(err, ErrUDSNotSupported) {
		t.Errorf("err = %v, want ErrUDSNotSupported", err)
	}
}

func TestParseKWPDTCsTruncated(t *testing.T) {
	if _, err := ParseKWPDTCs("57025A6020"); err == nil {
		t.Error("a reply announcing two codes and carrying one was accepted")
	}
}
