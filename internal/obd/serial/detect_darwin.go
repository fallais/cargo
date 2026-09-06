package serial

import (
	"path/filepath"
	"sort"
	"strings"
)

// candidatePatterns cover how ELM327 adapters present themselves on macOS.
//
// The cu.* ("call-up") devices are the right ones to open: the matching tty.*
// device blocks on open until it sees carrier detect, which a diagnostic
// adapter never asserts. The suffixes are the usual USB-serial bridges,
// CDC-ACM clones, and Bluetooth adapters, which appear under the name they
// were paired with.
var candidatePatterns = []string{
	"/dev/cu.usbserial*",
	"/dev/cu.usbmodem*",
	"/dev/cu.SLAB_USBtoUART*",
	"/dev/cu.wchusbserial*",
	"/dev/cu.*OBD*",
	"/dev/cu.*obd*",
}

// detectPlatformSerialDev returns the first plausible adapter device.
func detectPlatformSerialDev() string {
	if ports := listPlatformSerialDevs(); len(ports) > 0 {
		return ports[0]
	}
	return "/dev/cu.usbserial"
}

// describeDevice has nothing to add on macOS: the device name already
// carries the bridge or the paired name.
func describeDevice(string) string { return "" }

// candidateDescription says where we looked, for an error message.
func candidateDescription() string {
	return strings.Join(candidatePatterns, ", ")
}

// listPlatformSerialDevs returns every device that might be an adapter.
func listPlatformSerialDevs() []string {
	seen := make(map[string]struct{})
	var found []string

	for _, pattern := range candidatePatterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, m := range matches {
			// The OBD patterns overlap the bridge patterns, so the same
			// device can match twice.
			if _, dup := seen[m]; dup {
				continue
			}
			seen[m] = struct{}{}
			found = append(found, m)
		}
	}

	sort.Strings(found)
	return found
}
