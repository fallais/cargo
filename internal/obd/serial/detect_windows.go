package serial

import "fmt"

// detectPlatformSerialDev returns the first COM port worth trying.
//
// Windows offers no cheap enumeration through this serial package, so the
// candidate list is positional and Open walks it.
func detectPlatformSerialDev() string {
	return "COM3"
}

// candidateDescription says where we looked, for an error message.
func candidateDescription() string {
	return "COM1 through COM16"
}

// listPlatformSerialDevs returns the COM ports an adapter is usually bound to.
func listPlatformSerialDevs() []string {
	ports := make([]string, 0, 16)
	for i := 1; i <= 16; i++ {
		ports = append(ports, fmt.Sprintf("COM%d", i))
	}
	return ports
}
