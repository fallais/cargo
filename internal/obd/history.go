package obd

import (
	"fmt"
	"strconv"
	"strings"
)

// FaultHistory is what a module's extended data says about a fault's past:
// how often it happened, and at what mileage. That is what separates a fault
// that keeps coming back from one that happened once, years ago. Fields the
// module did not report are -1.
type FaultHistory struct {
	Occurrences int
	// FirstKm and LastKm are odometer readings. A module that keeps a
	// single reading reports it as LastKm.
	FirstKm int
	LastKm  int
}

// Known reports whether anything was read at all.
func (h FaultHistory) Known() bool {
	return h.Occurrences >= 0 || h.FirstKm >= 0 || h.LastKm >= 0
}

// renaultGroup are the marques whose ECUs share the Renault record layout.
var renaultGroup = map[string]bool{"renault": true, "dacia": true}

// History interprets the extended records for a make.
//
// The record numbers are the manufacturer's, so a layout is only applied to
// the make it was worked out on. Elsewhere only the widespread convention of
// record 0x01 opening with an occurrence count is used.
func (d DTCDetail) History(make string) FaultHistory {
	h := FaultHistory{Occurrences: -1, FirstKm: -1, LastKm: -1}

	if renaultGroup[strings.ToLower(make)] {
		// Worked out on a 2015 Sandero, not documented. The Bosch engine
		// ECU keeps 0x84 as a one-byte count and 0x87, 0x80 and 0x85 as
		// three-byte odometer readings: the first occurrence, the start
		// and the end of the latest one. The fit is tight: a fault seen
		// once has all three equal and a count of 1, the clutch switch
		// with a count of 13 spans 145,176 to 155,408 km, and no reading
		// on any module exceeded the odometer when it was read. The body
		// controller, cluster and ABS keep only 0x80.
		if r, ok := d.record(0x84, 1); ok {
			h.Occurrences = int(r[0])
		}
		if r, ok := d.record(0x87, 3); ok {
			h.FirstKm = km(r)
		}
		if r, ok := d.record(0x85, 3); ok {
			h.LastKm = km(r)
		} else if r, ok := d.record(0x80, 3); ok {
			h.LastKm = km(r)
		}
		return h
	}

	for _, r := range d.Records {
		if r.Number == 0x01 && len(r.Data) > 0 {
			h.Occurrences = int(r.Data[0])
		}
	}
	return h
}

// record returns a record that holds exactly size bytes.
func (d DTCDetail) record(number byte, size int) ([]byte, bool) {
	for _, r := range d.Records {
		if r.Number == number && len(r.Data) == size {
			return r.Data, true
		}
	}
	return nil, false
}

func km(b []byte) int {
	return int(b[0])<<16 | int(b[1])<<8 | int(b[2])
}

// String renders what is known, e.g. "13 times · first 145,176 km · last
// 155,408 km". A single mileage reads "at 187,017 km".
func (h FaultHistory) String() string {
	var parts []string
	switch {
	case h.Occurrences == 1:
		parts = append(parts, "once")
	case h.Occurrences >= 0:
		parts = append(parts, fmt.Sprintf("%d times", h.Occurrences))
	}
	switch {
	case h.FirstKm >= 0 && h.LastKm >= 0 && h.FirstKm != h.LastKm:
		parts = append(parts, "first "+thousands(h.FirstKm)+" km", "last "+thousands(h.LastKm)+" km")
	case h.LastKm >= 0:
		parts = append(parts, "at "+thousands(h.LastKm)+" km")
	case h.FirstKm >= 0:
		parts = append(parts, "first "+thousands(h.FirstKm)+" km")
	}
	return strings.Join(parts, " · ")
}

// thousands groups digits in threes, as an odometer reads.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
