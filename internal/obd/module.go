package obd

import (
	"fmt"
	"strings"
)

// A Module is an ECU we can address individually.
type Module struct {
	Name string
	// Request is the CAN identifier we address (ATSH).
	Request uint16
	// Response is what it replies on, used as a receive filter (ATCRA) so
	// another talkative ECU is not mistaken for this one.
	//
	// Zero means "ask the module". Only the emissions addresses have a
	// response identifier fixed by standard; everywhere else the offset is
	// a manufacturer convention, and guessing it wrong discards the reply
	// in a way that is indistinguishable from an absent module.
	Response uint16
	// Standard marks the emissions modules ISO 15765-4 guarantees. Those
	// answer the OBD-II modes, and may keep further faults over UDS; the
	// rest answer UDS 0x19 or KWP2000 only.
	Standard bool
	// Manufacturer is the protocol the module's own fault memory answered
	// in the last scan, so that clearing it goes the same way.
	Manufacturer Protocol
	// Readable is whether its fault memory answered in the last scan. A
	// sweep finds modules that answer to their address but read no faults.
	Readable bool
}

// Identity is what a module says about itself. Any field may be empty: the
// UDS identifiers are standard, but not every module implements them.
type Identity struct {
	PartNumber string // UDS F187, the manufacturer's spare part number
	Hardware   string // F191
	Software   string // F195
	Supplier   string // F18A
	System     string // F197, the module's own name for what it is
	// Other is identification in a form that is not standardised, such as
	// the KWP2000 local identifier 0x80, given as its readable runs.
	Other string
}

// String joins what is known, most specific first.
func (i Identity) String() string {
	var parts []string
	for _, p := range []string{i.PartNumber, i.Supplier, i.System} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if i.Software != "" {
		parts = append(parts, "sw "+i.Software)
	}
	if len(parts) == 0 && i.Other != "" {
		parts = append(parts, i.Other)
	}
	return strings.Join(parts, " · ")
}

// Protocol is a manufacturer diagnostic protocol.
type Protocol uint8

const (
	// NoProtocol means the module answered only the OBD-II modes.
	NoProtocol Protocol = iota
	UDS
	KWP2000
)

func (m Module) String() string {
	return fmt.Sprintf("%s (%03X)", m.Name, m.Request)
}

// Functional is the broadcast address every emissions ECU listens on. With
// headers suppressed the replies cannot be told apart, which is why the scan
// addresses modules individually instead.
const Functional uint16 = 0x7DF

// StandardModules are the emissions controllers from ISO 15765-4: requests
// 0x7E0-0x7E7, replies offset by 8.
var StandardModules = []Module{
	{Name: "Engine", Request: 0x7E0, Response: 0x7E8, Standard: true},
	{Name: "Transmission", Request: 0x7E1, Response: 0x7E9, Standard: true},
	{Name: "ECU 3", Request: 0x7E2, Response: 0x7EA, Standard: true},
	{Name: "ECU 4", Request: 0x7E3, Response: 0x7EB, Standard: true},
}

// ExtendedModules are addresses manufacturers commonly use for the
// non-emissions controllers. They are conventions, not standards, so a
// non-answer means "not present here" rather than a fault.
//
// Response is left zero throughout: where these modules reply is discovered by
// asking, because the offset differs by make. ISO 15765-4 puts the emissions
// replies at request+8; PSA and Renault group answer at request+0x20.
//
// The names are the usual occupant of each address and not much more. The
// module reports its own faults either way, so a wrong label costs a column,
// not a code.
var ExtendedModules = []Module{
	{Name: "Airbag", Request: 0x740},
	{Name: "Module 742", Request: 0x742},
	{Name: "Module 743", Request: 0x743},
	{Name: "Body Control", Request: 0x745},
	{Name: "Module 752", Request: 0x752},
	{Name: "ABS", Request: 0x760},
	{Name: "Instrument Cluster", Request: 0x720},
	{Name: "TPMS", Request: 0x7C0},
}

// AllModules is the scan order, guaranteed modules first so an interrupted
// scan still returns what matters most.
func AllModules() []Module {
	all := make([]Module, 0, len(StandardModules)+len(ExtendedModules))
	all = append(all, StandardModules...)
	return append(all, ExtendedModules...)
}
