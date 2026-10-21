package serial

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/fallais/cargo/internal/dtc"
)

// KWP2000 (ISO 14230-3) carried over CAN.
//
// The Renault group kept it on modules that predate its move to UDS, so one
// car can mix the two: on a 2015 Sandero the body controller answers UDS 0x19
// while the power steering refuses it and answers 0x17 instead. Both share the
// negative-response framing, which is why the UDS error mapping serves here.
const (
	// kwpReadStatusOfDTC reports the stored codes of a group. Renault
	// modules answer it where the standard's 0x18 is refused.
	kwpReadStatusOfDTC byte = 0x17

	// kwpAllGroups selects every code the module keeps.
	kwpAllGroups uint16 = 0xFF00

	// The KWP2000 sessions. 0xC0 is in the manufacturer range and is the
	// one Renault tooling enters to read faults; the standard 0x81 is
	// where the module returns to.
	kwpSessionDefault    byte = 0x81
	kwpSessionDiagnostic byte = 0xC0
)

// readDTCsKWP asks one module for its stored faults over KWP2000.
//
// allowSession has the same meaning as for UDS: a module that refuses the read
// in its default session is asked again in a diagnostic one, then put back.
func (s *SerialOBD) readDTCsKWP(ctx context.Context, elm *ELM327, module string, allowSession bool) ([]dtc.DTC, error) {
	request := fmt.Sprintf("%02X%04X", kwpReadStatusOfDTC, kwpAllGroups)

	codes, err := s.udsAttempt(ctx, elm, request, ParseKWPDTCs)
	if err == nil {
		return attributeModule(codes, module), nil
	}
	if !allowSession || !needsSession(err) {
		return nil, err
	}

	slog.Debug("Retrying in a KWP2000 diagnostic session",
		"module", module, "error", err)

	if sessionErr := setSession(ctx, elm, kwpSessionDiagnostic); sessionErr != nil {
		return nil, err // report the original refusal, not the retry's
	}
	defer func() {
		if err := setSession(ctx, elm, kwpSessionDefault); err != nil {
			slog.Debug("Could not restore the default session",
				"module", module, "error", err)
		}
	}()

	codes, err = s.udsAttempt(ctx, elm, request, ParseKWPDTCs)
	if err != nil {
		return nil, err
	}
	return attributeModule(codes, module), nil
}

// ParseKWPDTCs decodes a service 0x17 reply: a count, then three-byte records
// of two code bytes and a status byte.
//
// The codes use the same J2012 encoding as OBD-II. The status byte does not
// use the UDS bits, and what Renault puts in it is not documented, so it is
// not interpreted: StatusMask stays zero rather than claiming a lamp or a live
// failure the byte may not mean. Every listed code is a stored fault; the
// service reports nothing else.
func ParseKWPDTCs(response string) ([]dtc.DTC, error) {
	if err := responseError(response); err != nil {
		return nil, err
	}
	data, err := normalizeHex(response)
	if err != nil {
		return nil, err
	}

	positive := kwpReadStatusOfDTC + 0x40
	for len(data) >= 3 && data[0] == negativeResponse && data[1] == kwpReadStatusOfDTC &&
		data[2] == nrcResponsePending {
		data = data[3:]
	}
	if len(data) >= 3 && data[0] == negativeResponse && data[1] == kwpReadStatusOfDTC {
		return nil, udsError(kwpReadStatusOfDTC, data[2])
	}
	if len(data) < 2 || data[0] != positive {
		return nil, fmt.Errorf("%w: no response to service %02X in % X",
			ErrParse, kwpReadStatusOfDTC, data)
	}

	count, records := int(data[1]), data[2:]
	if available := len(records) / 3; count > available {
		return nil, fmt.Errorf("%w: %d codes announced, %d present",
			ErrUDSNoStatusBytes, count, available)
	}

	codes := make([]dtc.DTC, 0, count)
	for i := 0; i < count; i++ {
		a, b := records[3*i], records[3*i+1]
		if a == 0 && b == 0 {
			continue // filler, not a fault on code P0000
		}
		d := dtc.Decode(a, b)
		d.Status = dtc.Stored
		codes = append(codes, d)
	}
	return codes, nil
}
