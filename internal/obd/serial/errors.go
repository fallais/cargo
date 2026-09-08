package serial

import (
	"errors"
	"fmt"
	"strings"
)

// Errors an ELM327 reports as plain text rather than as a protocol failure.
// They are values so callers can branch with errors.Is instead of matching on
// substrings of a formatted message.
var (
	ErrNotConnected    = errors.New("not connected")
	ErrNoData          = errors.New("no data: the ECU did not answer this request")
	ErrUnableToConnect = errors.New("unable to connect: no vehicle bus detected")
	ErrBusError        = errors.New("bus error")
	ErrBusInit         = errors.New("bus initialisation failed")
	ErrCANError        = errors.New("CAN error")
	ErrStopped         = errors.New("command interrupted")
	ErrBadCommand      = errors.New("adapter did not understand the command")
	ErrBufferFull      = errors.New("adapter buffer overflow")
	ErrLowVoltage      = errors.New("supply voltage too low")
	ErrParse           = errors.New("cannot parse adapter response")
	ErrTimeout         = errors.New("timed out waiting for adapter")
	// ErrBusy means another request holds the adapter. One serial line
	// carries everything, so this is contention, not a fault.
	ErrBusy = errors.New("adapter busy")
)

// statusResponses maps the adapter's text replies onto sentinels. Order
// matters: "BUS INIT: ERROR" must be tested before the bare "ERROR".
var statusResponses = []struct {
	text string
	err  error
}{
	{"UNABLE TO CONNECT", ErrUnableToConnect},
	{"BUS INIT", ErrBusInit},
	{"BUS ERROR", ErrBusError},
	{"CAN ERROR", ErrCANError},
	{"BUFFER FULL", ErrBufferFull},
	{"LV RESET", ErrLowVoltage},
	{"NO DATA", ErrNoData},
	{"NODATA", ErrNoData},
	{"STOPPED", ErrStopped},
	{"ERROR", ErrBusError},
	{"?", ErrBadCommand},
}

// responseError reports whether a reply is a status message rather than data.
//
// This has to run before any hex parsing, because several of these strings are
// themselves valid hex ("NO DATA" contains D, A, A) and would otherwise be
// silently decoded into nonsense.
func responseError(response string) error {
	upper := strings.ToUpper(strings.TrimSpace(response))
	if upper == "" {
		return fmt.Errorf("%w: empty response", ErrParse)
	}

	// The adapter emits this while negotiating a protocol; it is progress,
	// not a result, and the caller should keep reading.
	upper = strings.TrimSpace(strings.ReplaceAll(upper, "SEARCHING...", ""))
	if upper == "" {
		return fmt.Errorf("%w: adapter still searching for a protocol", ErrNoData)
	}

	for _, s := range statusResponses {
		if strings.Contains(upper, s.text) {
			return s.err
		}
	}
	return nil
}
