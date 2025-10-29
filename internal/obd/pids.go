package obd

import "fmt"

type PID struct {
	Mode string
	Code string
	Desc string
}

var (
	PIDCoolantTemp     = PID{Mode: "01", Code: "05", Desc: "Engine Coolant Temperature"}
	PIDEngineRPM       = PID{Mode: "01", Code: "0C", Desc: "Engine RPM"}
	PIDVehicleSpeed    = PID{Mode: "01", Code: "0D", Desc: "Vehicle Speed"}
	PIDOilTemp         = PID{Mode: "01", Code: "5C", Desc: "Engine Oil Temperature"}
	PIDTotalKilometers = PID{Mode: "01", Code: "31", Desc: "Distance traveled since codes cleared"}
	PIDDTCCount        = PID{Mode: "03", Code: "00", Desc: "Number of stored DTCs"}
)

func (p PID) String() string {
	return fmt.Sprintf("%s%s", p.Mode, p.Code)
}
