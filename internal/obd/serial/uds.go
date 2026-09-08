package serial

import (
	"context"
	"errors"
	"fmt"

	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"log/slog"
)

// UDS (ISO 14229-1) service and sub-function identifiers.
//
// The non-emissions controllers, ABS and airbag among them, do not answer the
// OBD-II modes at all: those are an emissions mandate and stop at the
// powertrain. Everything else speaks UDS, where reading faults is service 0x19.
const (
	udsReadDTCInformation byte = 0x19
	udsDiagnosticSession  byte = 0x10

	// subReportDTCByStatusMask asks for the codes currently set, which is
	// what a fault read means. The neighbouring 0x0A reports every code the
	// ECU could ever set, which is a capability list, not a fault list.
	subReportDTCByStatusMask byte = 0x02

	sessionDefault  byte = 0x01
	sessionExtended byte = 0x03

	// negativeResponse prefixes every rejection.
	negativeResponse byte = 0x7F
)

// Negative response codes worth telling apart. The rest are reported by number.
const (
	nrcServiceNotSupported     byte = 0x11
	nrcSubFunctionNotSupported byte = 0x12
	nrcBusyRepeatRequest       byte = 0x21
	nrcConditionsNotCorrect    byte = 0x22
	nrcRequestOutOfRange       byte = 0x31
	nrcSecurityAccessDenied    byte = 0x33
	nrcResponsePending         byte = 0x78
)

// Errors a UDS request can produce.
var (
	ErrUDSNotSupported  = errors.New("module does not support this UDS service")
	ErrUDSConditions    = errors.New("module refused: conditions not correct")
	ErrUDSSecurity      = errors.New("module refused: security access required")
	ErrUDSOutOfRange    = errors.New("module refused: request out of range")
	ErrUDSBusy          = errors.New("module busy")
	ErrUDSStillPending  = errors.New("module kept asking for more time")
	ErrUDSNegative      = errors.New("module returned a negative response")
	ErrUDSNoStatusBytes = errors.New("truncated UDS trouble-code record")
)

// udsError maps a negative response code onto an error.
func udsError(service, nrc byte) error {
	switch nrc {
	case nrcServiceNotSupported, nrcSubFunctionNotSupported:
		return fmt.Errorf("%w (service %02X, NRC %02X)", ErrUDSNotSupported, service, nrc)
	case nrcConditionsNotCorrect:
		return ErrUDSConditions
	case nrcSecurityAccessDenied:
		return ErrUDSSecurity
	case nrcRequestOutOfRange:
		return ErrUDSOutOfRange
	case nrcBusyRepeatRequest:
		return ErrUDSBusy
	default:
		return fmt.Errorf("%w: NRC %02X", ErrUDSNegative, nrc)
	}
}

// udsPayload finds the positive response to a service and returns what follows
// its sub-function byte.
//
// Two things make this more than a byte search. An ECU may answer 7F <svc> 78
// to say "still working", possibly several times, before the real reply, and
// both can arrive in one read; those have to be skipped rather than treated as
// failures. And matching the service echo together with its sub-function makes
// a false positive on a data byte far less likely than matching 0x59 alone.
func udsPayload(data []byte, service, subFunction byte) ([]byte, error) {
	positive := service + 0x40

	for i := 0; i+1 < len(data); i++ {
		if data[i] == negativeResponse && data[i+1] == service {
			if i+2 >= len(data) {
				return nil, fmt.Errorf("%w: truncated", ErrUDSNegative)
			}
			nrc := data[i+2]
			if nrc == nrcResponsePending {
				i += 2 // keep looking; the real answer may follow
				continue
			}
			return nil, udsError(service, nrc)
		}

		if data[i] == positive && data[i+1] == subFunction {
			return data[i+2:], nil
		}
	}

	return nil, fmt.Errorf("%w: no response to service %02X sub-function %02X",
		ErrParse, service, subFunction)
}

// ParseUDSDTCs decodes a service 0x19 sub-function 0x02 reply.
//
// The payload is a one-byte availability mask followed by four-byte records:
// three bytes of trouble code and one status byte. UDS codes are three bytes
// where OBD-II uses two, the extra byte saying how the component failed rather
// than which one did.
func ParseUDSDTCs(response string) ([]dtc.DTC, error) {
	if err := responseError(response); err != nil {
		return nil, err
	}

	data, err := normalizeHex(response)
	if err != nil {
		return nil, err
	}

	rest, err := udsPayload(data, udsReadDTCInformation, subReportDTCByStatusMask)
	if err != nil {
		return nil, err
	}
	if len(rest) < 1 {
		return nil, fmt.Errorf("%w: no availability mask", ErrUDSNoStatusBytes)
	}

	// The availability mask says which status bits this ECU maintains. It is
	// not a code, so step past it.
	records := rest[1:]

	codes := make([]dtc.DTC, 0, len(records)/4)
	for i := 0; i+3 < len(records); i += 4 {
		a, b, failureType, status := records[i], records[i+1], records[i+2], records[i+3]

		// Frames are padded to a boundary; an all-zero record is filler,
		// not a fault on a nonexistent component.
		if a == 0 && b == 0 && failureType == 0 && status == 0 {
			continue
		}

		// Asking with a 0xFF mask returns every code the module holds a
		// slot for, and most of them are not faults: a record whose only
		// set bits are "not tested this cycle" is the ECU listing what it
		// could report, not what it is reporting. One airbag module
		// answers with forty of those, which would fill the table with
		// invented problems.
		if status&faultBits == 0 {
			continue
		}
		codes = append(codes, dtc.DecodeUDS(a, b, failureType, status))
	}

	return codes, nil
}

// faultBits are the status bits that make a record an actual fault rather than
// a slot the module keeps for one. Everything else - not tested, not completed
// - describes the test, not the component.
const faultBits = dtc.StatusTestFailed | dtc.StatusTestFailedThisCycle |
	dtc.StatusPending | dtc.StatusConfirmed | dtc.StatusTestFailedSinceClear

// readDTCsUDS asks one module for its faults over UDS.
//
// statusMask selects which codes to report. 0xFF asks for everything and lets
// the per-code status byte do the filtering, which is more portable than
// trusting every ECU to implement the same subset of mask bits.
func (s *SerialOBD) readDTCsUDS(ctx context.Context, elm *ELM327, module string, statusMask byte) ([]dtc.DTC, error) {
	request := fmt.Sprintf("%02X%02X%02X", udsReadDTCInformation, subReportDTCByStatusMask, statusMask)

	codes, err := s.udsAttempt(ctx, elm, request)
	if err == nil {
		return attributeModule(codes, module), nil
	}

	// Many chassis and body controllers only answer diagnostics in an
	// extended session. Entering one is a state change on the ECU, so it is
	// a fallback rather than the default: a module that answers in the
	// default session is never disturbed.
	if !errors.Is(err, ErrUDSNotSupported) && !errors.Is(err, ErrUDSConditions) {
		return nil, err
	}

	slog.Debug("Retrying in an extended diagnostic session",
		"module", module, "error", err)

	if sessionErr := setSession(ctx, elm, sessionExtended); sessionErr != nil {
		return nil, err // report the original refusal, not the retry's
	}
	// Leave the ECU as we found it whether or not the read succeeds.
	defer func() {
		if err := setSession(ctx, elm, sessionDefault); err != nil {
			slog.Debug("Could not restore the default session",
				"module", module, "error", err)
		}
	}()

	codes, err = s.udsAttempt(ctx, elm, request)
	if err != nil {
		return nil, err
	}
	return attributeModule(codes, module), nil
}

// udsAttempt sends one request, retrying while the module asks for more time.
//
// A module that answers 0x78 is working on it, and the real reply may land
// after the adapter has already returned. Re-reading is the only way to
// collect it.
func (s *SerialOBD) udsAttempt(ctx context.Context, elm *ELM327, request string) ([]dtc.DTC, error) {
	const maxPending = 3

	var lastErr error
	for attempt := 0; attempt < maxPending; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		response, err := elm.Query(ctx, request)
		if err != nil {
			s.noteQueryFailure(err)
			return nil, err
		}

		codes, err := ParseUDSDTCs(response)
		if err == nil {
			return codes, nil
		}
		lastErr = err

		// Anything other than "still working" will not improve on a retry.
		if !isResponsePending(response) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("%w: %v", ErrUDSStillPending, lastErr)
}

// isResponsePending reports whether a reply was only the module asking for
// more time.
func isResponsePending(response string) bool {
	data, err := normalizeHex(response)
	if err != nil {
		return false
	}
	for i := 0; i+2 < len(data); i++ {
		if data[i] == negativeResponse && data[i+2] == nrcResponsePending {
			return true
		}
	}
	return false
}

// setSession switches an ECU's diagnostic session.
func setSession(ctx context.Context, elm *ELM327, session byte) error {
	response, err := elm.Query(ctx, fmt.Sprintf("%02X%02X", udsDiagnosticSession, session))
	if err != nil {
		return err
	}
	if err := responseError(response); err != nil {
		return err
	}

	data, err := normalizeHex(response)
	if err != nil {
		return err
	}
	if _, err := udsPayload(data, udsDiagnosticSession, session); err != nil {
		return err
	}
	return nil
}

func attributeModule(codes []dtc.DTC, module string) []dtc.DTC {
	for i := range codes {
		codes[i].Module = module
	}
	return codes
}

// Sub-functions of service 0x19 that report what a module kept about a fault
// beyond the fact of it.
const (
	subReportDTCSnapshot     byte = 0x04
	subReportDTCExtendedData byte = 0x06

	// allRecords asks for every record the module holds.
	allRecords byte = 0xFF
	// occurrenceRecord is, by near-universal convention, the extended data
	// record whose first byte counts how many times the fault has happened.
	occurrenceRecord byte = 0x01
)

// DTCDetail reads the occurrence counter and freeze-frame for one code.
func (s *SerialOBD) DTCDetail(ctx context.Context, code dtc.DTC) (obd.DTCDetail, error) {
	detail := obd.DTCDetail{Occurrences: -1}

	elm, err := s.adapter()
	if err != nil {
		return detail, err
	}

	module, ok := s.moduleNamed(code.Module)
	if !ok {
		return detail, fmt.Errorf("%w: no module named %q in the last scan",
			ErrNoData, code.Module)
	}
	if module.Standard {
		// The emissions modules answer mode 02 for freeze-frame data, not
		// service 0x19. Saying so beats sending a request that cannot work.
		return detail, fmt.Errorf("%w: %s speaks the OBD-II modes, whose "+
			"freeze frame is mode 02", ErrUDSNotSupported, module.Name)
	}

	if err := s.acquireBus(ctx); err != nil {
		return detail, err
	}
	defer s.releaseBus()

	if err := elm.SetHeader(ctx, module.Request); err != nil {
		return detail, err
	}
	if err := elm.SetReceiveFilter(ctx, module.Response); err != nil {
		return detail, err
	}
	defer func() {
		if err := elm.ClearReceiveFilter(ctx); err != nil {
			slog.Debug("Restoring receive filter", "error", err)
		}
		if err := elm.SetHeader(ctx, obd.Functional); err != nil {
			slog.Warn("Restoring broadcast header", "error", err)
		}
	}()

	if err := elm.SetFlowControl(ctx, module.Request); err != nil {
		slog.Debug("Adapter would not set flow control, asking anyway", "error", err)
	} else {
		defer func() {
			if err := elm.ClearFlowControl(ctx); err != nil {
				slog.Debug("Restoring flow control", "error", err)
			}
		}()
	}
	if err := elm.SetResponseTimeout(ctx, udsResponseTimeout); err != nil {
		slog.Debug("Could not extend the adapter timeout", "error", err)
	}
	defer func() {
		if err := elm.SetResponseTimeout(ctx, defaultResponseTimeout); err != nil {
			slog.Debug("Restoring the adapter timeout", "error", err)
		}
	}()

	hi, mid, lo := udsBytes(code)

	// The two reads are independent: a module may keep a counter and no
	// snapshot, or the reverse, and one refusal should not hide the other.
	if response, err := elm.Query(ctx, fmt.Sprintf("%02X%02X%02X%02X%02X%02X",
		udsReadDTCInformation, subReportDTCExtendedData, hi, mid, lo, occurrenceRecord)); err == nil {
		if count, raw, err := ParseUDSExtendedData(response); err == nil {
			detail.Occurrences, detail.Extended = count, raw
		} else {
			slog.Debug("No extended data", "code", code.FullCode(), "error", err)
		}
	}

	if response, err := elm.Query(ctx, fmt.Sprintf("%02X%02X%02X%02X%02X%02X",
		udsReadDTCInformation, subReportDTCSnapshot, hi, mid, lo, allRecords)); err == nil {
		if identifiers, raw, err := ParseUDSSnapshot(response); err == nil {
			detail.SnapshotIdentifiers, detail.Snapshot = identifiers, raw
		} else {
			slog.Debug("No snapshot", "code", code.FullCode(), "error", err)
		}
	}

	return detail, nil
}

// udsBytes rebuilds the three bytes a code arrived as. Raw keeps the first
// two and FailureType is the third, so nothing has to be re-encoded from the
// printed form.
func udsBytes(code dtc.DTC) (hi, mid, lo byte) {
	return byte(code.Raw >> 8), byte(code.Raw), code.FailureType
}

// moduleNamed finds the module a code was attributed to.
func (s *SerialOBD) moduleNamed(name string) (obd.Module, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.modules {
		if m.Name == name {
			return m, true
		}
	}
	return obd.Module{}, false
}

// ParseUDSExtendedData decodes a service 0x19 sub-function 0x06 reply.
//
// The reply is the code echoed back, its status, then the record number and
// the record itself. Only the framing is standard: what the record contains
// is the manufacturer's, so the first byte is read as the occurrence count by
// convention and the whole record is returned alongside for anyone who knows
// better.
func ParseUDSExtendedData(response string) (int, []byte, error) {
	if err := responseError(response); err != nil {
		return -1, nil, err
	}
	data, err := normalizeHex(response)
	if err != nil {
		return -1, nil, err
	}

	rest, err := udsPayload(data, udsReadDTCInformation, subReportDTCExtendedData)
	if err != nil {
		return -1, nil, err
	}
	// Three bytes of code, one of status, one record number.
	if len(rest) < 5 {
		return -1, nil, fmt.Errorf("%w: extended data record is %d bytes",
			ErrUDSNoStatusBytes, len(rest))
	}

	record := rest[5:]
	if len(record) == 0 {
		return -1, nil, fmt.Errorf("%w: record %02X carried no data",
			ErrUDSNoStatusBytes, rest[4])
	}
	return int(record[0]), record, nil
}

// ParseUDSSnapshot decodes a service 0x19 sub-function 0x04 reply.
//
// Past the header this is a list of data identifiers whose lengths only the
// ECU knows, so the payload is returned whole rather than split into values
// that would be wrong wherever the guess at a length was.
func ParseUDSSnapshot(response string) (int, []byte, error) {
	if err := responseError(response); err != nil {
		return 0, nil, err
	}
	data, err := normalizeHex(response)
	if err != nil {
		return 0, nil, err
	}

	rest, err := udsPayload(data, udsReadDTCInformation, subReportDTCSnapshot)
	if err != nil {
		return 0, nil, err
	}
	// Three bytes of code, one of status, one record number, one count.
	if len(rest) < 6 {
		return 0, nil, fmt.Errorf("%w: snapshot record is %d bytes",
			ErrUDSNoStatusBytes, len(rest))
	}
	return int(rest[5]), rest[6:], nil
}
