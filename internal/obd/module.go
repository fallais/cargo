package obd

import "fmt"

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
	// Standard marks the emissions modules ISO 15765-4 guarantees. It also
	// picks the protocol: those answer OBD-II modes, the rest UDS 0x19.
	Standard bool
}

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
