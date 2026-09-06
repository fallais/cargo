package serial

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"cargo/internal/dtc"
)

// normalizeHex flattens an ELM327 reply into the bytes it carries.
//
// The adapter's output format shifts under us: ATS0 removes the spaces between
// bytes, and a reply too long for one CAN frame comes back as a total-length
// line followed by ordinal-prefixed continuation lines:
//
//	014
//	0: 43 03 01 33 01
//	1: 71 02 34 00 00
//
// Rather than trusting any particular spacing, strip the framing and keep the
// hex digits. Callers must run responseError first: status replies like
// "NO DATA" contain hex digits and would decode into plausible-looking rubbish.
func normalizeHex(response string) ([]byte, error) {
	lines := strings.FieldsFunc(response, func(r rune) bool {
		return r == '\r' || r == '\n'
	})

	// A continuation ordinal anywhere means the first line is the total
	// length rather than data.
	multiframe := false
	for _, line := range lines {
		if strings.Contains(line, ":") {
			multiframe = true
			break
		}
	}

	var digits strings.Builder
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if idx := strings.IndexByte(line, ':'); idx >= 0 {
			line = line[idx+1:]
		} else if multiframe && i == 0 {
			continue // total-length header
		}

		for _, r := range line {
			if isHexDigit(r) {
				digits.WriteRune(r)
			}
		}
	}

	s := digits.String()
	if s == "" {
		return nil, fmt.Errorf("%w: no hex payload in %q", ErrParse, response)
	}
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("%w: odd hex digit count in %q", ErrParse, response)
	}

	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	return b, nil
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// payload finds the response to a given service and returns the bytes after
// it. A positive reply carries the request mode plus 0x40, so mode 01 answers
// with 0x41 and mode 03 with 0x43.
func payload(data []byte, mode byte) ([]byte, bool) {
	want := mode + 0x40
	for i, b := range data {
		if b == want {
			return data[i+1:], true
		}
	}
	return nil, false
}

// parsePID extracts the data bytes for a mode 01 PID, checking that the echoed
// PID matches what was asked for. Without that check a reply that arrives late
// gets attributed to whichever request is in flight.
func parsePID(response string, pid byte, want int) ([]byte, error) {
	if err := responseError(response); err != nil {
		return nil, err
	}

	data, err := normalizeHex(response)
	if err != nil {
		return nil, err
	}

	rest, ok := payload(data, 0x01)
	if !ok {
		return nil, fmt.Errorf("%w: no mode 01 reply in %q", ErrParse, response)
	}
	if len(rest) < 1+want {
		return nil, fmt.Errorf("%w: mode 01 reply is %d bytes, want %d", ErrParse, len(rest), 1+want)
	}
	if rest[0] != pid {
		return nil, fmt.Errorf("%w: reply is for PID %02X, expected %02X", ErrParse, rest[0], pid)
	}

	return rest[1 : 1+want], nil
}

// ParseRPM decodes PID 010C. Engine speed arrives as a quarter-RPM count.
func ParseRPM(response string) (int, error) {
	b, err := parsePID(response, 0x0C, 2)
	if err != nil {
		return 0, err
	}
	return (int(b[0])*256 + int(b[1])) / 4, nil
}

// ParseTemp decodes the shared temperature encoding used by PID 0105 (coolant)
// and 015C (oil): one byte offset by 40 so it can express -40C upward.
func ParseTemp(response string, pid byte) (float64, error) {
	b, err := parsePID(response, pid, 1)
	if err != nil {
		return 0, err
	}
	return float64(int(b[0]) - 40), nil
}

// ParseSpeed decodes PID 010D, already in km/h.
func ParseSpeed(response string) (int, error) {
	b, err := parsePID(response, 0x0D, 1)
	if err != nil {
		return 0, err
	}
	return int(b[0]), nil
}

// ParseDistance decodes PID 0131, distance since the codes were last cleared.
func ParseDistance(response string) (int, error) {
	b, err := parsePID(response, 0x31, 2)
	if err != nil {
		return 0, err
	}
	return int(b[0])*256 + int(b[1]), nil
}

// ParseVoltage reads an ATRV reply such as "12.5V".
func ParseVoltage(response string) (float64, error) {
	s := strings.TrimSpace(strings.ToUpper(response))
	s = strings.TrimSuffix(s, "V")
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("%w: voltage %q: %v", ErrParse, response, err)
	}
	return v, nil
}

// ParseDTCs decodes a reply to mode 03 (stored), 07 (pending) or 0A
// (permanent) into trouble codes.
//
// Whether a count byte follows the mode echo depends on the protocol: CAN
// sends one, the older buses instead return a fixed three code slots. Protocol
// detection on a cheap adapter is not reliable enough to key off, so infer it
// from the payload length. A count byte makes the remainder odd (1 + 2n);
// without one it is even.
func ParseDTCs(response string, mode byte, status dtc.Status) ([]dtc.DTC, error) {
	if err := responseError(response); err != nil {
		return nil, err
	}

	data, err := normalizeHex(response)
	if err != nil {
		return nil, err
	}

	rest, ok := payload(data, mode)
	if !ok {
		return nil, fmt.Errorf("%w: no mode %02X reply in %q", ErrParse, mode, response)
	}

	limit := -1
	if len(rest)%2 == 1 {
		count := int(rest[0])
		rest = rest[1:]
		limit = count
		if count == 0 {
			return nil, nil // the ECU reports no codes
		}
	}

	codes := make([]dtc.DTC, 0, len(rest)/2)
	for i := 0; i+1 < len(rest); i += 2 {
		a, b := rest[i], rest[i+1]
		if a == 0 && b == 0 {
			continue // padding to the frame boundary
		}

		d := dtc.Decode(a, b)
		d.Status = status
		codes = append(codes, d)

		if limit > 0 && len(codes) >= limit {
			break
		}
	}

	return codes, nil
}
