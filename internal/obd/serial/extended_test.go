package serial

import (
	"context"
	"testing"

	"github.com/fallais/cargo/internal/obd"
)

// Replies read from a 2015 Sandero's Bosch engine ECU for P0833-29. Asked for
// every record it answers them back to back; asked for one, it says how long
// that one is. The engine first answers "still working" to almost everything.
var sanderoP0833 = map[string][]string{
	"1906083329FF": {"7F1978", "018\r0:590608332928\r1:80025F0C840D85\r2:025F1086000058\r3:87023718AAAAAA"},
	"190608332980": {"00A\r0:590608332928\r1:80025F0CAAAAAA"},
	"190608332984": {"008\r0:590608332928\r1:840DAAAAAAAAAA"},
	"190608332985": {"00A\r0:590608332928\r1:85025F10AAAAAA"},
	"190608332986": {"00A\r0:590608332928\r1:86000058AAAAAA"},
	"190608332987": {"00A\r0:590608332928\r1:87023718AAAAAA"},
}

func TestReadExtendedRecordsSplitsByAskingPerRecord(t *testing.T) {
	elm, _ := newScriptedELM(t, sanderoP0833)
	s := &SerialOBD{}

	records, err := s.readExtendedRecords(context.Background(), elm, 0x08, 0x33, 0x29)
	if err != nil {
		t.Fatalf("readExtendedRecords: %v", err)
	}

	want := []struct {
		number byte
		data   string
	}{
		{0x80, "025F0C"}, {0x84, "0D"}, {0x85, "025F10"}, {0x86, "000058"}, {0x87, "023718"},
	}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d: %+v", len(records), len(want), records)
	}
	for i, w := range want {
		if records[i].Number != w.number || hexString(records[i].Data) != w.data {
			t.Errorf("record %d = %02X %s, want %02X %s",
				i, records[i].Number, hexString(records[i].Data), w.number, w.data)
		}
	}

	h := obd.DTCDetail{Records: records}.History("dacia")
	if h.Occurrences != 13 || h.FirstKm != 145176 || h.LastKm != 155408 {
		t.Errorf("history = %+v, want 13 times from 145,176 to 155,408 km", h)
	}
}

// A record whose length cannot be learned is kept with everything after it,
// rather than split at a guess.
func TestReadExtendedRecordsKeepsTheRestWhenUnsure(t *testing.T) {
	elm, _ := newScriptedELM(t, map[string][]string{
		"1906083329FF": {"018\r0:590608332928\r1:80025F0C840D85\r2:025F1086000058\r3:87023718AAAAAA"},
		"190608332980": {"00A\r0:590608332928\r1:80025F0CAAAAAA"},
		"190608332984": {"7F1931"},
	})
	s := &SerialOBD{}

	records, err := s.readExtendedRecords(context.Background(), elm, 0x08, 0x33, 0x29)
	if err != nil {
		t.Fatalf("readExtendedRecords: %v", err)
	}
	if len(records) != 2 || records[1].Number != 0x84 || len(records[1].Data) != 13 {
		t.Errorf("got %+v, want 80 then 84 holding the remaining 13 bytes", records)
	}
}

// A module that refuses "all records" may still keep the conventional one.
func TestReadExtendedRecordsFallsBackToRecordOne(t *testing.T) {
	elm, _ := newScriptedELM(t, map[string][]string{
		"19065A607BFF": {"7F1931"},
		"19065A607B01": {"59065A607BA8010C0300"},
	})
	s := &SerialOBD{}

	records, err := s.readExtendedRecords(context.Background(), elm, 0x5A, 0x60, 0x7B)
	if err != nil {
		t.Fatalf("readExtendedRecords: %v", err)
	}
	if len(records) != 1 || records[0].Number != 0x01 || hexString(records[0].Data) != "0C0300" {
		t.Errorf("got %+v, want record 01 holding 0C0300", records)
	}
}

func TestOnlyPending(t *testing.T) {
	for in, want := range map[string]bool{
		"7F1978":               true,
		"7F1978\r7F1978":       true,
		"7F1978\r590608332928": false,
		"590608332928":         false,
		"7F1931":               false,
	} {
		if got := onlyPending(in); got != want {
			t.Errorf("onlyPending(%q) = %v, want %v", in, got, want)
		}
	}
}

// With the engine stopped, the Sandero's engine ECU said nothing to the OBD-II
// modes and still answered UDS. Its faults must not be lost to the silence.
func TestStandardModuleReadOverUDSWhenModesAreSilent(t *testing.T) {
	s, _ := newScriptedProvider(t, map[string][]string{
		"1902FF": {"7F1978", "007\r0:5902FF083329\r1:28AAAAAAAAAAAA"},
	})
	m := obd.Module{Name: "Engine", Request: 0x7E0, Response: 0x7E8, Standard: true}

	codes, err := s.readModule(context.Background(), s.elm, &m)
	if err != nil {
		t.Fatalf("readModule: %v", err)
	}
	if len(codes) != 1 || codes[0].FullCode() != "P0833-29" {
		t.Errorf("codes = %v, want P0833-29", codes)
	}
	if m.Manufacturer != obd.UDS {
		t.Errorf("protocol = %v, want UDS", m.Manufacturer)
	}
}

// An address with nothing on it stays absent.
func TestStandardModuleAbsentWhenBothSilent(t *testing.T) {
	s, _ := newScriptedProvider(t, nil)
	m := obd.Module{Name: "ECU 3", Request: 0x7E2, Response: 0x7EA, Standard: true}

	if _, err := s.readModule(context.Background(), s.elm, &m); err == nil {
		t.Error("an empty address was read as a module")
	}
}
