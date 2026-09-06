package serial

import (
	"path/filepath"
	"sort"
	"strings"
)

// candidatePatterns cover how ELM327 adapters present themselves on Linux:
// USB serial bridges (ttyUSB), CDC-ACM clones (ttyACM), and Bluetooth adapters
// bound with rfcomm.
var candidatePatterns = []string{
	"/dev/ttyUSB*",
	"/dev/ttyACM*",
	"/dev/rfcomm*",
}

// detectPlatformSerialDev returns the first plausible adapter device.
//
// It returns a path even when nothing matches, so the caller can report which
// device it tried rather than an empty string.
func detectPlatformSerialDev() string {
	if ports := listPlatformSerialDevs(); len(ports) > 0 {
		return ports[0]
	}
	return "/dev/ttyUSB0"
}

// candidateDescription says where we looked, for an error message.
func candidateDescription() string {
	return strings.Join(candidatePatterns, ", ")
}

// listPlatformSerialDevs returns every device that might be an adapter.
func listPlatformSerialDevs() []string {
	var found []string
	for _, pattern := range candidatePatterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		found = append(found, matches...)
	}
	sort.Strings(found)
	return found
}
