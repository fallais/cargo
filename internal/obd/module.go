package obd

import "fmt"

// A Module is an ECU we can address individually.
type Module struct {
	Name string
	// Request is the CAN identifier we address (ATSH).
	Request uint16
	// Response is what it replies on, used as a receive filter (ATCRA) so
	// another talkative ECU is not mistaken for this one.
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
var ExtendedModules = []Module{
	{Name: "ABS", Request: 0x760, Response: 0x768},
	{Name: "Airbag", Request: 0x740, Response: 0x748},
	{Name: "Body Control", Request: 0x745, Response: 0x74D},
	{Name: "Instrument Cluster", Request: 0x720, Response: 0x728},
	{Name: "TPMS", Request: 0x7C0, Response: 0x7C8},
}

// AllModules is the scan order, guaranteed modules first so an interrupted
// scan still returns what matters most.
func AllModules() []Module {
	all := make([]Module, 0, len(StandardModules)+len(ExtendedModules))
	all = append(all, StandardModules...)
	return append(all, ExtendedModules...)
}
