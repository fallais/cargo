package obd

import "testing"

func rec(number byte, data ...byte) ExtendedRecord { return ExtendedRecord{Number: number, Data: data} }

func TestHistoryRenaultEngine(t *testing.T) {
	// P1589-29 as the Sandero's engine ECU keeps it.
	d := DTCDetail{Records: []ExtendedRecord{
		rec(0x80, 0x02, 0xDC, 0x15), rec(0x84, 0x04), rec(0x85, 0x02, 0xDC, 0x4C),
		rec(0x86, 0x00, 0x00, 0x93), rec(0x87, 0x02, 0x91, 0xD8),
	}}

	h := d.History("Dacia")
	if h.Occurrences != 4 || h.FirstKm != 168408 || h.LastKm != 187468 {
		t.Errorf("history = %+v", h)
	}
	if got, want := h.String(), "4 times · first 168,408 km · last 187,468 km"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// The body controller keeps a single mileage.
func TestHistoryRenaultSingleReading(t *testing.T) {
	d := DTCDetail{Records: []ExtendedRecord{rec(0x80, 0x02, 0xDA, 0x89)}}

	h := d.History("renault")
	if h.Occurrences != -1 || h.FirstKm != -1 || h.LastKm != 187017 {
		t.Errorf("history = %+v", h)
	}
	if got, want := h.String(), "at 187,017 km"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// A layout worked out on one make is not applied to another.
func TestHistoryOtherMakesUseOnlyRecordOne(t *testing.T) {
	d := DTCDetail{Records: []ExtendedRecord{rec(0x84, 0x0D), rec(0x01, 0x03, 0xFF)}}

	h := d.History("ford")
	if h.Occurrences != 3 || h.FirstKm != -1 || h.LastKm != -1 {
		t.Errorf("history = %+v, want only the record 01 count", h)
	}
	if got := (DTCDetail{}).History("ford"); got.Known() {
		t.Errorf("no records gave %+v", got)
	}
}

// A record of an unexpected size is not read as a count or a mileage.
func TestHistoryIgnoresUnexpectedSizes(t *testing.T) {
	d := DTCDetail{Records: []ExtendedRecord{rec(0x84, 0x0D, 0x00), rec(0x80, 0x02)}}
	if h := d.History("dacia"); h.Known() {
		t.Errorf("history = %+v, want nothing", h)
	}
}

func TestHistoryOnce(t *testing.T) {
	h := FaultHistory{Occurrences: 1, FirstKm: 188056, LastKm: 188056}
	if got, want := h.String(), "once · at 188,056 km"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
