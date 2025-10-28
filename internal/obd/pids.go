package obd

var pidDescriptions = map[string]string{
	"0100": "PIDs supported [01-20]",
	"0101": "Monitor status since DTCs cleared",
	"0105": "Engine coolant temperature",
	"010C": "Engine RPM",
	"010D": "Vehicle speed",
	"010F": "Intake air temperature",
	"0110": "MAF air flow rate",
	"0120": "PIDs supported [21-40]",
	"0133": "Absolute barometric pressure",
	"0142": "Control module voltage",
	"0173": "Distance traveled since codes cleared",
}
