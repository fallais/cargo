//go:build !linux && !darwin && !windows

package serial

// The BSDs and anything else Go targets are not tested here, so rather than
// failing to compile they get no autodetection: --port still works.

func detectPlatformSerialDev() string { return "" }

func candidateDescription() string {
	return "nothing (autodetection is not implemented on this platform, pass --port)"
}

func listPlatformSerialDevs() []string { return nil }

func describeDevice(string) string { return "" }
