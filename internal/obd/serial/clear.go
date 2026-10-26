package serial

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fallais/cargo/internal/obd"
)

// Requests that erase a module's fault memory. Each protocol clears with its
// own, so a module is cleared the way it was read.
const (
	// obdClear is mode 04: stored and pending codes, freeze frames and the
	// MIL. Permanent codes are not touched.
	obdClear byte = 0x04

	// clearDiagnosticInformation is 0x14 in both UDS and KWP2000. They
	// differ only in the group argument: three bytes in UDS, two in KWP.
	clearDiagnosticInformation byte = 0x14
)

var (
	udsClearAll = fmt.Sprintf("%02XFFFFFF", clearDiagnosticInformation)
	kwpClearAll = fmt.Sprintf("%02X%04X", clearDiagnosticInformation, kwpAllGroups)
)

// ClearDTCs erases the codes of the named modules.
//
// Only modules found by the last scan can be cleared: the scan is what learns
// where each one answers and over which protocol, and a clear sent any other
// way is at best refused.
func (s *SerialOBD) ClearDTCs(ctx context.Context, modules []string) []obd.ClearResult {
	results := make([]obd.ClearResult, 0, len(modules))
	fail := func(err error) []obd.ClearResult {
		for _, name := range modules {
			results = append(results, obd.ClearResult{Module: name, Err: err})
		}
		return results
	}

	elm, err := s.adapter()
	if err != nil {
		return fail(err)
	}
	if err := s.acquireBus(ctx); err != nil {
		return fail(err)
	}
	defer s.releaseBus()

	// A pre-CAN bus has no addressing to speak of, and its scan reported
	// every code unattributed. Mode 04 to everyone is the only option.
	if !elm.IsCAN() {
		err := clearOBD(ctx, elm)
		for _, name := range modules {
			results = append(results, obd.ClearResult{Module: name, Err: err})
		}
		return results
	}

	defer func() {
		if err := elm.ClearReceiveFilter(ctx); err != nil {
			slog.Warn("Restoring receive filter", "error", err)
		}
		if err := elm.SetResponseTimeout(ctx, defaultResponseTimeout); err != nil {
			slog.Debug("Restoring the adapter timeout", "error", err)
		}
		if err := elm.SetHeader(ctx, obd.Functional); err != nil {
			slog.Warn("Restoring broadcast header", "error", err)
		}
	}()

	for _, name := range modules {
		if err := ctx.Err(); err != nil {
			results = append(results, obd.ClearResult{Module: name, Err: err})
			continue
		}

		module, ok := s.moduleNamed(name)
		if !ok {
			results = append(results, obd.ClearResult{Module: name,
				Err: fmt.Errorf("%w: no module named %q in the last scan", ErrNoData, name)})
			continue
		}

		err := s.clearModule(ctx, elm, module)
		if err == nil {
			slog.Info("Trouble codes cleared", "module", module.String())
		}
		results = append(results, obd.ClearResult{Module: name, Err: err})
	}
	return results
}

// clearModule erases one module's codes.
//
// An emissions module gets mode 04 and, if it also kept faults over UDS or
// KWP2000, that protocol's clear too, in its default session only: the same
// restraint the scan shows a running powertrain. The others are cleared in
// whichever protocol they were read with, entering a diagnostic session if
// they refuse outside one.
func (s *SerialOBD) clearModule(ctx context.Context, elm *ELM327, m obd.Module) error {
	if err := elm.SetHeader(ctx, m.Request); err != nil {
		return fmt.Errorf("address %s: %w", m, err)
	}
	if err := elm.SetReceiveFilter(ctx, m.Response); err != nil {
		return fmt.Errorf("filter %s: %w", m, err)
	}
	// Erasing takes a module longer than reading does.
	if err := elm.SetResponseTimeout(ctx, udsResponseTimeout); err != nil {
		slog.Debug("Could not extend the adapter timeout", "error", err)
	}

	var errs []error
	if m.Standard || m.Manufacturer == obd.NoProtocol {
		if err := clearOBD(ctx, elm); err != nil {
			errs = append(errs, fmt.Errorf("mode 04: %w", err))
		}
	}

	allowSession := !m.Standard
	switch m.Manufacturer {
	case obd.UDS:
		if err := s.clearInSession(ctx, elm, m.Name, udsClearAll,
			sessionExtended, sessionDefault, allowSession); err != nil {
			errs = append(errs, fmt.Errorf("UDS: %w", err))
		}
	case obd.KWP2000:
		if err := s.clearInSession(ctx, elm, m.Name, kwpClearAll,
			kwpSessionDiagnostic, kwpSessionDefault, allowSession); err != nil {
			errs = append(errs, fmt.Errorf("KWP2000: %w", err))
		}
	}
	return errors.Join(errs...)
}

// clearOBD sends mode 04 to whatever the adapter is addressing.
func clearOBD(ctx context.Context, elm *ELM327) error {
	response, err := elm.Query(ctx, fmt.Sprintf("%02X", obdClear))
	if err != nil {
		return err
	}
	return parseAck(response, obdClear)
}

// clearInSession sends a 0x14 clear, retrying in a diagnostic session if the
// module refuses outside one and that is allowed.
func (s *SerialOBD) clearInSession(ctx context.Context, elm *ELM327, module, request string,
	enter, restore byte, allowSession bool) error {
	err := s.ackAttempt(ctx, elm, request, clearDiagnosticInformation)
	if err == nil || !allowSession || !needsSession(err) {
		return err
	}

	slog.Debug("Retrying the clear in a diagnostic session", "module", module, "error", err)
	if sessionErr := setSession(ctx, elm, enter); sessionErr != nil {
		return err // report the original refusal, not the retry's
	}
	defer func() {
		if err := setSession(ctx, elm, restore); err != nil {
			slog.Debug("Could not restore the default session",
				"module", module, "error", err)
		}
	}()

	return s.ackAttempt(ctx, elm, request, clearDiagnosticInformation)
}

// ackAttempt sends a request whose positive reply carries nothing but the
// echo, retrying while the module asks for more time. Erasing memory is the
// slowest thing a module is asked to do, so "still working" is common here.
// Sending the clear again is harmless: there is nothing left to erase twice.
func (s *SerialOBD) ackAttempt(ctx context.Context, elm *ELM327, request string, service byte) error {
	const maxPending = 3

	var lastErr error
	for attempt := 0; attempt < maxPending; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		response, err := elm.Query(ctx, request)
		if err != nil {
			s.noteQueryFailure(err)
			return err
		}

		err = parseAck(response, service)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isResponsePending(response) {
			return err
		}
	}
	return fmt.Errorf("%w: %v", ErrUDSStillPending, lastErr)
}

// parseAck checks the reply to a request that answers with a bare echo: 0x44
// for mode 04, 0x54 for a 0x14 clear.
func parseAck(response string, service byte) error {
	if err := responseError(response); err != nil {
		return err
	}
	data, err := normalizeHex(response)
	if err != nil {
		return err
	}

	for len(data) >= 3 && data[0] == negativeResponse && data[1] == service &&
		data[2] == nrcResponsePending {
		data = data[3:]
	}
	if len(data) >= 3 && data[0] == negativeResponse && data[1] == service {
		return udsError(service, data[2])
	}
	if len(data) < 1 || data[0] != service+0x40 {
		return fmt.Errorf("%w: no acknowledgement of service %02X in %q",
			ErrParse, service, response)
	}
	return nil
}
