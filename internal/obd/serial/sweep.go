package serial

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/fallais/cargo/internal/obd"
)

// The diagnostic range a sweep walks. Requests go out on 700-7FF; the
// identifiers reserved for replies to the emissions modules and the broadcast
// address are skipped, since nothing is addressed there.
const (
	sweepFirst uint16 = 0x700
	sweepLast  uint16 = 0x7FF
)

// sweepAddresses lists the identifiers a sweep asks on.
func sweepAddresses() []uint16 {
	var out []uint16
	for id := sweepFirst; id <= sweepLast; id++ {
		if id == obd.Functional || (id >= 0x7E8 && id <= 0x7EF) {
			continue
		}
		out = append(out, id)
	}
	return out
}

// SweepModules looks for a module at every diagnostic address rather than at
// the usual few, and makes what it finds the module list that scans and
// clears then use.
//
// Which modules a car has is not something OBD-II can be asked: only the
// emissions modules have to answer the broadcast. Dealer tools carry a list
// per model; without one, the only way to know is to ask every address. That
// sends a request to identifiers that may carry ordinary traffic, so the
// caller should only do it with the engine off.
func (s *SerialOBD) SweepModules(ctx context.Context, progress func(obd.ScanProgress)) ([]obd.Module, error) {
	elm, err := s.adapter()
	if err != nil {
		return nil, err
	}
	if !elm.IsCAN() {
		return nil, fmt.Errorf("a sweep needs a CAN bus; this one is %s", elm.ProtocolName())
	}
	if err := s.acquireBus(ctx); err != nil {
		return nil, err
	}
	defer s.releaseBus()
	defer restoreBroadcast(ctx, elm)

	known := map[uint16]obd.Module{}
	for _, m := range obd.AllModules() {
		known[m.Request] = m
	}

	addresses := sweepAddresses()
	var found []obd.Module
	for i, id := range addresses {
		if err := ctx.Err(); err != nil {
			return found, err
		}
		report(progress, obd.ScanProgress{
			Module:   fmt.Sprintf("address %03X", id),
			Index:    i + 1,
			Total:    len(addresses),
			Answered: len(found),
		})

		m, ok := known[id]
		if !ok {
			m = obd.Module{Name: fmt.Sprintf("Module %03X", id), Request: id}
		}
		// Where it replies is learned here, not assumed: the table's
		// fixed replies are for the emissions convention only.
		m.Response = 0
		m.Standard = id >= 0x7E0 && id <= 0x7E7

		if err := elm.SetHeader(ctx, id); err != nil {
			return found, err
		}
		response, err := s.discoverResponse(ctx, elm, m)
		if err != nil {
			if !errors.Is(err, ErrNoData) {
				slog.Debug("Sweep: no module", "request", fmt.Sprintf("%03X", id), "error", err)
			}
			continue
		}
		m.Response = response
		slog.Info("Sweep: module found",
			"request", fmt.Sprintf("%03X", id), "responds_on", fmt.Sprintf("%03X", response))
		found = append(found, m)
	}

	report(progress, obd.ScanProgress{
		Index: len(addresses), Total: len(addresses), Answered: len(found), Done: true,
	})

	if len(found) > 0 {
		s.mu.Lock()
		s.modules = found
		s.scanned = true
		s.mu.Unlock()
	}
	return found, nil
}

// Modules returns the modules the last scan or sweep settled on, with what it
// learned about each.
func (s *SerialOBD) Modules() []obd.Module {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]obd.Module(nil), s.modules...)
}

// UDS data identifiers a module identifies itself with (ISO 14229-1 annex C).
var identityDIDs = []struct {
	did string
	set func(*obd.Identity, string)
}{
	{"F187", func(i *obd.Identity, v string) { i.PartNumber = v }},
	{"F191", func(i *obd.Identity, v string) { i.Hardware = v }},
	{"F195", func(i *obd.Identity, v string) { i.Software = v }},
	{"F18A", func(i *obd.Identity, v string) { i.Supplier = supplierName(v) }},
	{"F197", func(i *obd.Identity, v string) { i.System = v }},
}

// kwpIdentification is the KWP2000 local identifier Renault modules answer
// with their references.
const kwpIdentification = "2180"

// Identify asks a module from the last scan or sweep what it is.
//
// Only reads are sent and the session is never changed, so a module that will
// only identify itself in a diagnostic session stays unidentified.
func (s *SerialOBD) Identify(ctx context.Context, name string) (obd.Identity, error) {
	var id obd.Identity

	elm, err := s.adapter()
	if err != nil {
		return id, err
	}
	module, ok := s.moduleNamed(name)
	if !ok {
		return id, fmt.Errorf("%w: no module named %q in the last scan", ErrNoData, name)
	}
	if err := s.acquireBus(ctx); err != nil {
		return id, err
	}
	defer s.releaseBus()
	defer restoreBroadcast(ctx, elm)

	if err := elm.SetHeader(ctx, module.Request); err != nil {
		return id, err
	}
	if err := elm.SetReceiveFilter(ctx, module.Response); err != nil {
		return id, err
	}
	if err := elm.SetFlowControl(ctx, module.Request); err == nil {
		defer func() {
			if err := elm.ClearFlowControl(ctx); err != nil {
				slog.Debug("Restoring flow control", "error", err)
			}
		}()
	}
	if err := elm.SetResponseTimeout(ctx, udsResponseTimeout); err == nil {
		defer func() {
			if err := elm.SetResponseTimeout(ctx, defaultResponseTimeout); err != nil {
				slog.Debug("Restoring the adapter timeout", "error", err)
			}
		}()
	}

	for _, d := range identityDIDs {
		response, err := queryAnswered(ctx, elm, "22"+d.did)
		if err != nil {
			continue
		}
		if value, err := ParseIdentifier(response, d.did); err == nil && value != "" {
			d.set(&id, value)
		}
	}

	if id == (obd.Identity{}) {
		if response, err := queryAnswered(ctx, elm, kwpIdentification); err == nil {
			if value, err := ParseKWPIdentification(response); err == nil {
				id.Other = value
			}
		}
	}
	return id, nil
}

// ParseIdentifier decodes a positive reply to UDS 0x22 for one text identifier.
func ParseIdentifier(response, did string) (string, error) {
	if err := responseError(response); err != nil {
		return "", err
	}
	data, err := normalizeHex(response)
	if err != nil {
		return "", err
	}
	if len(data) >= 3 && data[0] == negativeResponse {
		return "", udsError(data[1], data[2])
	}
	want := fmt.Sprintf("62%s", did)
	if len(data) < 3 || fmt.Sprintf("%02X%02X%02X", data[0], data[1], data[2]) != want {
		return "", fmt.Errorf("%w: no reply to 22%s in %q", ErrParse, did, response)
	}
	return readableText(data[3:]), nil
}

// ParseKWPIdentification decodes a reply to KWP2000 0x21 0x80. Its layout is
// the manufacturer's, so only the readable runs are kept: on a Renault module
// those are its references.
func ParseKWPIdentification(response string) (string, error) {
	if err := responseError(response); err != nil {
		return "", err
	}
	data, err := normalizeHex(response)
	if err != nil {
		return "", err
	}
	if len(data) >= 3 && data[0] == negativeResponse {
		return "", udsError(data[1], data[2])
	}
	if len(data) < 2 || data[0] != 0x61 || data[1] != 0x80 {
		return "", fmt.Errorf("%w: no reply to 2180 in %q", ErrParse, response)
	}

	var runs []string
	for _, run := range strings.FieldsFunc(string(data[2:]), func(r rune) bool { return r < 0x20 || r > 0x7E }) {
		if run = strings.TrimSpace(run); len(run) >= 4 {
			runs = append(runs, run)
		}
	}
	return strings.Join(runs, " "), nil
}

// readableText keeps the printable part of an identifier, which modules pad
// with spaces, zeros or 0xFF.
func readableText(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c >= 0x20 && c <= 0x7E {
			out = append(out, c)
		} else if len(out) > 0 && out[len(out)-1] != ' ' {
			out = append(out, ' ')
		}
	}
	return strings.TrimSpace(string(out))
}

// supplierName shortens a supplier identifier to the name, dropping the tax
// and supplier numbers that follow it: "Robert Bosch GmbH, VAT ID No ...".
func supplierName(s string) string {
	if i := strings.IndexByte(s, ','); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// restoreBroadcast puts the adapter back to addressing every emissions module.
func restoreBroadcast(ctx context.Context, elm *ELM327) {
	if err := elm.ClearReceiveFilter(ctx); err != nil {
		slog.Warn("Restoring receive filter", "error", err)
	}
	if err := elm.SetHeader(ctx, obd.Functional); err != nil {
		slog.Warn("Restoring broadcast header", "error", err)
	}
}
