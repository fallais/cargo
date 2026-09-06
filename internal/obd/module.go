package obd

import "fmt"

// A Module is an ECU we can address individually.
//
// Cars carry dozens of controllers, but they are not equally reachable. ISO
// 15765-4 assigns 0x7E0-0x7E7 for requests to the emissions-related units and
// 0x7E8-0x7EF for their replies, and those are the only ones the OBD-II modes
// are required to answer. Everything else - ABS, airbag, body, TPMS - sits at
// manufacturer-chosen addresses and usually answers UDS service 0x19 rather
// than mode 03, so scanning it with standard OBD-II is best-effort.
type Module struct {
	Name string
	// Request is the CAN identifier we address (ATSH).
	Request uint16
	// Response is the identifier the module replies on, used to set a
	// receive filter (ATCRA) so another talkative ECU cannot be mistaken
	// for this one.
	Response uint16
	// Standard marks the modules ISO 15765-4 guarantees. Non-standard
	// entries are common conventions, not requirements: a miss on one is
	// normal and must not be reported as a fault.
	Standard bool
}

func (m Module) String() string {
	return fmt.Sprintf("%s (%03X)", m.Name, m.Request)
}

// Functional is the broadcast address every emissions ECU listens on. A
// request here reaches all of them at once, but with headers suppressed the
// replies cannot be told apart, which is why the scan addresses modules
// individually instead.
const Functional uint16 = 0x7DF

// StandardModules are the emissions-related controllers defined by
// ISO 15765-4. Requests run 0x7E0-0x7E7 with replies offset by 8.
var StandardModules = []Module{
	{Name: "Engine", Request: 0x7E0, Response: 0x7E8, Standard: true},
	{Name: "Transmission", Request: 0x7E1, Response: 0x7E9, Standard: true},
	{Name: "ECU 3", Request: 0x7E2, Response: 0x7EA, Standard: true},
	{Name: "ECU 4", Request: 0x7E3, Response: 0x7EB, Standard: true},
}

// ExtendedModules are addresses many manufacturers use for the non-emissions
// controllers. They are conventions rather than standards and vary by make, so
// treat a non-answer as "not present at this address" rather than an error.
//
// Note that reaching one of these is only half the problem: most answer UDS
// 0x19 (ReadDTCInformation), not OBD-II mode 03, so a reply here is not
// guaranteed to be parseable by this tool.
var ExtendedModules = []Module{
	{Name: "ABS", Request: 0x760, Response: 0x768},
	{Name: "Airbag", Request: 0x740, Response: 0x748},
	{Name: "Body Control", Request: 0x745, Response: 0x74D},
	{Name: "Instrument Cluster", Request: 0x720, Response: 0x728},
	{Name: "TPMS", Request: 0x7C0, Response: 0x7C8},
}

// AllModules is the full scan order: guaranteed modules first so a scan that
// is interrupted still returns the results that matter most.
func AllModules() []Module {
	all := make([]Module, 0, len(StandardModules)+len(ExtendedModules))
	all = append(all, StandardModules...)
	return append(all, ExtendedModules...)
}
