package obd

import "fmt"

// PID identifies one OBD-II parameter: the service that carries it and the
// parameter number within that service.
type PID struct {
	Mode string
	Code string
	Desc string
}

var (
	PIDCoolantTemp  = PID{Mode: "01", Code: "05", Desc: "Engine Coolant Temperature"}
	PIDEngineRPM    = PID{Mode: "01", Code: "0C", Desc: "Engine RPM"}
	PIDVehicleSpeed = PID{Mode: "01", Code: "0D", Desc: "Vehicle Speed"}
	PIDOilTemp      = PID{Mode: "01", Code: "5C", Desc: "Engine Oil Temperature"}
	// PIDDistanceSinceClear is distance travelled since the trouble codes
	// were last erased - not the odometer. It resets to zero on every
	// clear, which made it read as a near-new car on any vehicle whose
	// codes had just been cleared. There is no odometer in the mandatory
	// mode 01 set; a real one lives behind a manufacturer identifier.
	PIDDistanceSinceClear = PID{Mode: "01", Code: "31", Desc: "Distance travelled since codes cleared"}
	PIDDTCCount           = PID{Mode: "03", Code: "00", Desc: "Number of stored DTCs"}
)

// String renders the PID as the request bytes sent to the adapter.
func (p PID) String() string {
	return fmt.Sprintf("%s%s", p.Mode, p.Code)
}
