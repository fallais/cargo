package serial

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"log/slog"
)

// dtcModes are the three questions worth asking each module, and what an
// answer from each one means.
var dtcModes = []struct {
	mode   byte
	status dtc.Status
}{
	{0x03, dtc.Stored},
	{0x07, dtc.Pending},
	{0x0A, dtc.Permanent},
}

// reconnectDelay is how long to wait between connection attempts while no
// adapter is present. Long enough not to spin on a missing device, short
// enough that plugging one in feels responsive.
const reconnectDelay = 3 * time.Second

// SerialOBD talks to a vehicle through an ELM327 adapter.
type SerialOBD struct {
	opts Options

	stop        context.CancelFunc
	sessionCtx  context.Context
	autoconnect bool

	// connecting is a 1-buffered channel held for the length of a
	// connection attempt.
	connecting chan struct{}

	mu        sync.RWMutex
	elm       *ELM327
	connected bool
	lastErr   error
	// modules is the scan list, narrowed to those that answered once we
	// have seen the vehicle: re-probing addresses that are not there costs
	// a timeout each on every refresh.
	modules []obd.Module
	scanned bool

	// bus is a 1-buffered channel used as a lock over the adapter. It is
	// not part of mu: mu guards fields for the length of a field access,
	// while this is held across whole request sequences.
	bus chan struct{}
}

// New creates a provider. Nothing is opened until Start.
func New(opts Options) *SerialOBD {
	return &SerialOBD{
		opts:       opts,
		modules:    obd.AllModules(),
		bus:        make(chan struct{}, 1),
		connecting: make(chan struct{}, 1),
	}
}

// connectTimeout bounds one attempt: opening the port, the reset and setup
// commands, then the protocol search, which alone is allowed fifteen seconds.
// Probing every candidate device at every baud rate has to fit inside this.
const connectTimeout = 45 * time.Second

// Start begins the session. It connects immediately only when autoconnect is
// on; otherwise it waits to be told which adapter to use.
//
// ctx must last for the whole session. It is what stops the supervisor, so a
// context that expires takes reconnection with it and an adapter plugged in
// afterwards would never be noticed. Each attempt gets a bounded child.
func (s *SerialOBD) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.stop = cancel
	s.sessionCtx = ctx
	auto := s.autoconnect
	s.mu.Unlock()

	go s.supervise(ctx)

	if !auto {
		return nil
	}
	return s.attempt(ctx, "")
}

// Connect attaches to a named adapter, replacing any current connection.
func (s *SerialOBD) Connect(ctx context.Context, port string) error {
	s.Disconnect()

	s.mu.RLock()
	session := s.sessionCtx
	s.mu.RUnlock()
	if session == nil {
		session = ctx
	}

	return s.attempt(session, port)
}

// Disconnect releases the adapter but leaves the session running, so the user
// can pick a different one.
func (s *SerialOBD) Disconnect() {
	s.mu.Lock()
	elm := s.elm
	s.elm = nil
	s.connected = false
	s.lastErr = nil
	s.mu.Unlock()

	if elm != nil {
		if err := elm.Close(); err != nil {
			slog.Debug("Closing adapter", "error", err)
		}
	}
}

// SetAutoconnect turns background reconnection on or off.
func (s *SerialOBD) SetAutoconnect(on bool) {
	s.mu.Lock()
	s.autoconnect = on
	s.mu.Unlock()
}

// Autoconnect reports whether background reconnection is on.
func (s *SerialOBD) Autoconnect() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoconnect
}

// Adapters lists the devices that could be connected to.
func (s *SerialOBD) Adapters() []obd.Adapter {
	s.mu.RLock()
	configured := s.opts.Port
	s.mu.RUnlock()

	s.mu.RLock()
	live := ""
	if s.connected && s.elm != nil {
		live = s.elm.portName
	}
	s.mu.RUnlock()

	ports := []string{configured}
	if configured == "" {
		ports = listPlatformSerialDevs()
	}

	adapters := make([]obd.Adapter, 0, len(ports))
	for _, port := range ports {
		adapters = append(adapters, obd.Adapter{
			Port:      port,
			Detail:    describeDevice(port),
			Connected: port == live,
		})
	}
	return adapters
}

// attempt runs one bounded connection attempt against a specific port, or any
// candidate when port is empty.
//
// Attempts are serialised. The supervisor ticks every few seconds while a
// first attempt may still be probing baud rates, and two of them open the same
// port independently: each ELM327 has its own lock, so nothing stops their AT
// commands interleaving and each reading the other's replies. That desynchro-
// nises every command from its answer, which is how a negotiated protocol came
// back as "OK", the link stopped looking like CAN, and a nine-module scan
// collapsed into one broadcast that found nothing.
func (s *SerialOBD) attempt(ctx context.Context, port string) error {
	select {
	case s.connecting <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.connecting }()

	// Another attempt may have connected while this one waited, in which
	// case there is nothing left to do unless a specific port was asked
	// for.
	if port == "" && s.IsConnected() {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	return s.connect(ctx, port)
}

// connect opens and negotiates, replacing any existing connection.
func (s *SerialOBD) connect(ctx context.Context, port string) error {
	s.mu.RLock()
	opts := s.opts
	s.mu.RUnlock()
	if port != "" {
		opts.Port = port
	}

	elm, err := Open(ctx, opts)
	if err != nil {
		s.setError(err)
		return err
	}

	if err := elm.Negotiate(ctx); err != nil {
		elm.Close()
		err = fmt.Errorf("negotiate protocol: %w", err)
		s.setError(err)
		return err
	}

	s.mu.Lock()
	s.elm = elm
	s.connected = true
	s.lastErr = nil
	// A different vehicle may be on the other end of a new connection, so
	// the narrowed module list from a previous session no longer applies.
	s.modules = obd.AllModules()
	s.scanned = false
	s.mu.Unlock()

	return nil
}

// supervise reconnects whenever the link is down, so an adapter plugged in
// after startup, or a cable knocked loose mid-session, both recover without
// restarting the program.
func (s *SerialOBD) supervise(ctx context.Context) {
	ticker := time.NewTicker(reconnectDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only reconnect on our own initiative when asked to.
			// Otherwise a user who deliberately disconnected would find
			// themselves reattached a few seconds later.
			if s.IsConnected() || !s.Autoconnect() {
				continue
			}
			if err := s.attempt(ctx, ""); err == nil {
				slog.Info("Adapter connected")
			}
		}
	}
}

func (s *SerialOBD) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
	s.lastErr = err
}

func (s *SerialOBD) Stop() {
	s.mu.Lock()
	elm := s.elm
	stop := s.stop
	s.elm = nil
	s.stop = nil
	s.sessionCtx = nil
	s.connected = false
	s.mu.Unlock()

	if stop != nil {
		stop()
	}
	if elm != nil {
		if err := elm.Close(); err != nil {
			slog.Warn("Closing adapter", "error", err)
		}
	}
}

func (s *SerialOBD) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

// Description says what we are attached to, or why we are not.
func (s *SerialOBD) Description() string {
	s.mu.RLock()
	elm, err := s.elm, s.lastErr
	s.mu.RUnlock()

	if elm != nil {
		return fmt.Sprintf("%s @ %s", elm.portName, elm.ProtocolName())
	}
	// The caller already shows whether we are connected, so say why not
	// rather than saying it twice.
	if err != nil {
		return err.Error()
	}
	return ""
}

// adapter returns the live connection, or ErrNotConnected.
// acquireBus takes exclusive use of the adapter, waiting until ctx expires.
//
// A request is a sequence, not a single command: point the adapter at a module
// with ATSH, filter its replies with ATCRA, then ask. Two callers interleaved
// on one serial line produce answers addressed to whoever set the header last,
// which is how a module scan and a two-second dashboard poll running together
// make both of them fail.
func (s *SerialOBD) acquireBus(ctx context.Context) error {
	select {
	case s.bus <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tryBus is acquireBus for callers that would rather be told no. A dashboard
// poll that waits behind a thirty-second module scan is a poll whose answer is
// half a minute stale by the time it arrives.
func (s *SerialOBD) tryBus() error {
	select {
	case s.bus <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}

func (s *SerialOBD) releaseBus() { <-s.bus }

func (s *SerialOBD) adapter() (*ELM327, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected || s.elm == nil {
		return nil, ErrNotConnected
	}
	return s.elm, nil
}

// pid runs a mode 01 request and hands the reply to a decoder.
func pid[T any](ctx context.Context, s *SerialOBD, cmd string, decode func(string) (T, error)) (T, error) {
	var zero T

	elm, err := s.adapter()
	if err != nil {
		return zero, err
	}

	if err := s.tryBus(); err != nil {
		return zero, err
	}
	defer s.releaseBus()

	resp, err := elm.Query(ctx, cmd)
	if err != nil {
		s.noteQueryFailure(err)
		return zero, err
	}
	return decode(resp)
}

// GetVIN reads the vehicle identification number.
//
// Not every vehicle answers: mode 09 is only mandatory from the 2005 model
// year, so a miss here is ordinary and the caller falls back to asking.
func (s *SerialOBD) GetVIN(ctx context.Context) (string, error) {
	return pid(ctx, s, "0902", ParseVIN)
}

// GetVoltage reads the adapter's supply, which is the battery at pin 16.
//
// This is an adapter command rather than a vehicle request, so it still shares
// the one serial line and takes the bus like any other read.
func (s *SerialOBD) GetVoltage(ctx context.Context) (float64, error) {
	elm, err := s.adapter()
	if err != nil {
		return 0, err
	}

	if err := s.tryBus(); err != nil {
		return 0, err
	}
	defer s.releaseBus()

	return elm.Voltage(ctx)
}

func (s *SerialOBD) GetRPM(ctx context.Context) (int, error) {
	return pid(ctx, s, obd.PIDEngineRPM.String(), ParseRPM)
}

func (s *SerialOBD) GetCoolantTemp(ctx context.Context) (float64, error) {
	return pid(ctx, s, obd.PIDCoolantTemp.String(), func(r string) (float64, error) {
		return ParseTemp(r, 0x05)
	})
}

func (s *SerialOBD) GetOilTemp(ctx context.Context) (float64, error) {
	return pid(ctx, s, obd.PIDOilTemp.String(), func(r string) (float64, error) {
		return ParseTemp(r, 0x5C)
	})
}

func (s *SerialOBD) GetDistanceSinceClear(ctx context.Context) (int, error) {
	return pid(ctx, s, obd.PIDDistanceSinceClear.String(), ParseDistance)
}

// GetDTCs walks every reachable module and collects its trouble codes.
//
// On CAN each module is addressed individually, because a broadcast with
// headers suppressed produces a pile of replies with no way to tell who sent
// which. On the older buses physical addressing is not available in the same
// form, so a single broadcast is all we can do and the codes are reported
// without module attribution rather than with a guessed one.
func (s *SerialOBD) GetDTCs(ctx context.Context, progress func(obd.ScanProgress)) ([]dtc.DTC, error) {
	elm, err := s.adapter()
	if err != nil {
		return nil, err
	}

	// The whole walk is one transaction. Releasing between modules would
	// let a dashboard poll retarget the adapter mid-scan.
	if err := s.acquireBus(ctx); err != nil {
		return nil, err
	}
	defer s.releaseBus()

	if !elm.IsCAN() {
		report(progress, obd.ScanProgress{Module: "broadcast", Index: 1, Total: 1})
		codes, err := s.readModes(ctx, elm, "")
		answered := 0
		if err == nil {
			answered = 1
		}
		report(progress, obd.ScanProgress{Index: 1, Total: 1, Answered: answered, Done: true})
		return codes, err
	}

	var (
		found   []dtc.DTC
		present []obd.Module
	)

	s.mu.RLock()
	modules, scanned := s.modules, s.scanned
	s.mu.RUnlock()

	for i, module := range modules {
		if err := ctx.Err(); err != nil {
			return found, err
		}

		report(progress, obd.ScanProgress{
			Module:   module.Name,
			Index:    i + 1,
			Total:    len(modules),
			Answered: len(present),
		})

		module := module // a copy: readModule fills in a discovered address
		codes, err := s.readModule(ctx, elm, &module)
		if err != nil {
			// A module that is not fitted, or that speaks a protocol we
			// do not, is an ordinary outcome on a scan across makes.
			// It is logged at Info because the alternative, an empty
			// table with no way to tell "healthy" from "never asked",
			// is the harder thing to debug.
			slog.Info("Module did not answer",
				"module", module.String(), "error", err)
			continue
		}

		slog.Info("Module answered",
			"module", module.String(), "codes", len(codes))

		present = append(present, module)
		found = append(found, codes...)
	}

	report(progress, obd.ScanProgress{
		Index:    len(modules),
		Total:    len(modules),
		Answered: len(present),
		Done:     true,
	})

	// Narrow future scans to what actually replied.
	if !scanned && len(present) > 0 {
		s.mu.Lock()
		s.modules = present
		s.scanned = true
		s.mu.Unlock()
		slog.Info("Module scan complete",
			"responding", len(present), "probed", len(modules))
	}

	if err := elm.ClearReceiveFilter(ctx); err != nil {
		slog.Warn("Restoring receive filter", "error", err)
	}
	if err := elm.ClearFlowControl(ctx); err != nil {
		slog.Debug("Restoring flow control", "error", err)
	}
	if err := elm.SetHeader(ctx, obd.Functional); err != nil {
		slog.Warn("Restoring broadcast header", "error", err)
	}

	return found, nil
}

// report calls a progress callback that the caller may not have supplied.
func report(progress func(obd.ScanProgress), p obd.ScanProgress) {
	if progress != nil {
		progress(p)
	}
}

// RescanModules widens the next scan back to every known address.
//
// GetDTCs narrows its list to the modules that answered, because re-probing an
// address that is not there costs a timeout on every automatic refresh. That
// is right for the background poll and wrong for someone who has just asked
// for a rescan: a module missed once - slow to wake, or asleep when we first
// looked - would otherwise stay excluded for the rest of the session.
func (s *SerialOBD) RescanModules() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.modules = obd.AllModules()
	s.scanned = false
}

// udsStatusMask asks for every code the module holds, letting the per-code
// status byte decide what each one means rather than depending on the module
// implementing the same mask bits as its neighbours.
const udsStatusMask byte = 0xFF

// udsResponseTimeout is roughly 400ms in the adapter's four-millisecond units.
// Chassis and body modules answer far more slowly than the powertrain.
const udsResponseTimeout byte = 0x64

// readModule points the adapter at one ECU and reads its codes.
//
// Which protocol to use is decided by what the module is, not by trying both:
// the emissions controllers answer the OBD-II modes, and everything else
// speaks UDS. Asking an airbag module for mode 03 gets silence, and asking the
// engine for UDS 0x19 disturbs it for no gain.
func (s *SerialOBD) readModule(ctx context.Context, elm *ELM327, m *obd.Module) ([]dtc.DTC, error) {
	if err := elm.SetHeader(ctx, m.Request); err != nil {
		return nil, fmt.Errorf("address %s: %w", m, err)
	}

	if m.Standard {
		if err := elm.SetReceiveFilter(ctx, m.Response); err != nil {
			return nil, fmt.Errorf("filter %s: %w", m, err)
		}
		return s.readModes(ctx, elm, m.Name)
	}

	// Locate the module first, while nothing is configured for it: with
	// the receive filter off and the headers on, whatever answers names
	// itself. Flow control and a longer timeout are settings for a
	// conversation with a known address, and asking with them already in
	// place, against an unfiltered bus, is what made every one of these
	// requests run out its five seconds.
	if m.Response == 0 {
		response, err := s.discoverResponse(ctx, elm, *m)
		if err != nil {
			return nil, fmt.Errorf("locate %s: %w", m, err)
		}
		slog.Info("Module located",
			"module", m.Name,
			"request", fmt.Sprintf("%03X", m.Request),
			"responds_on", fmt.Sprintf("%03X", response))
		m.Response = response
	}

	if err := elm.SetReceiveFilter(ctx, m.Response); err != nil {
		return nil, fmt.Errorf("filter %s: %w", m, err)
	}

	// A non-standard address wants the adapter told how to acknowledge a
	// multi-frame reply, and given longer to wait for one.
	//
	// Not every clone implements ATFCSM, and failing the module here meant
	// one unsupported AT command silently excluded every non-emissions ECU
	// from the scan. Flow control only matters once a reply spans frames,
	// so a refusal costs at worst a truncated answer from a talkative
	// module: ask anyway and let the request be the thing that decides.
	if err := elm.SetFlowControl(ctx, m.Request); err != nil {
		slog.Debug("Adapter would not set flow control, asking anyway",
			"module", m.Name, "error", err)
	} else {
		defer func() {
			if err := elm.ClearFlowControl(ctx); err != nil {
				slog.Debug("Could not restore flow control", "error", err)
			}
		}()
	}

	if err := elm.SetResponseTimeout(ctx, udsResponseTimeout); err != nil {
		slog.Debug("Could not extend the adapter timeout", "error", err)
	}
	defer func() {
		if err := elm.SetResponseTimeout(ctx, defaultResponseTimeout); err != nil {
			slog.Debug("Could not restore the adapter timeout", "error", err)
		}
	}()

	codes, err := s.readDTCsUDS(ctx, elm, m.Name, udsStatusMask)
	if err == nil {
		return codes, nil
	}

	// The address conventions and the protocol conventions do not always
	// agree: a few makes put a body controller on a non-standard address
	// that still answers the OBD-II modes. Falling back costs one request
	// on a module that has already declined the first choice.
	if errors.Is(err, ErrUDSNotSupported) {
		slog.Debug("Module rejected UDS, trying the OBD-II modes",
			"module", m.Name)
		return s.readModes(ctx, elm, m.Name)
	}
	return nil, err
}

// udsLocateRequest reads the VIN by identifier. It is sent only to see whether
// anyone answers: a module that does not implement it refuses in its own name,
// and a refusal locates it exactly as well as a reply would. Being a short
// exchange it costs one round trip rather than a module's whole fault list.
const udsLocateRequest = "22F190"

// responseOffsets are the conventions in use for where a module replies
// relative to where it is addressed.
//
// ISO 15765-4 fixes the emissions modules at request+8, and that offset is
// widely reused. The Renault group, Dacia included, answers at request+0x20.
// Nothing in a reply says which convention a car follows, so the only way to
// know is to listen on each in turn.
var responseOffsets = []uint16{0x08, 0x20}

// discoverResponse finds which identifier a module answers on, by asking with
// the receive filter set to each convention until one produces a reply.
//
// Listening with no filter at all would find it in a single request, but the
// adapter then reports every frame on the bus: on a live vehicle that is a
// continuous stream, and the reply never ends in a prompt. Filtering on a
// guess and retrying is slower by one round trip and actually terminates.
func (s *SerialOBD) discoverResponse(ctx context.Context, elm *ELM327, m obd.Module) (uint16, error) {
	var lastErr error = ErrNoData

	for _, offset := range responseOffsets {
		candidate := m.Request + offset

		if err := elm.SetReceiveFilter(ctx, candidate); err != nil {
			return 0, err
		}

		response, err := elm.Query(ctx, udsLocateRequest)
		if err != nil {
			lastErr = err
			continue
		}
		// NO DATA here means nothing replied on this identifier, which
		// is the answer for every convention the car does not use.
		if err := responseError(response); err != nil {
			lastErr = err
			continue
		}
		return candidate, nil
	}

	return 0, lastErr
}

// readModes asks one target for stored, pending and permanent codes.
func (s *SerialOBD) readModes(ctx context.Context, elm *ELM327, module string) ([]dtc.DTC, error) {
	var (
		found    []dtc.DTC
		answered bool
	)

	for _, m := range dtcModes {
		resp, err := elm.Query(ctx, fmt.Sprintf("%02X", m.mode))
		if err != nil {
			continue
		}

		codes, err := ParseDTCs(resp, m.mode, m.status)
		if err != nil {
			// NO DATA is the adapter reporting that nothing replied,
			// so there is no module at this address. A module that is
			// present and healthy says so explicitly with a zero count
			// - mode 03 answers 43 00 - which parses cleanly below.
			// Reading silence as health counted three empty powertrain
			// addresses as working ECUs on every scan of this car.
			if errors.Is(err, ErrNoData) {
				continue
			}
			// Mode 0A is not implemented on every ECU; a malformed
			// answer there is not worth failing the whole module for.
			slog.Debug("Unparseable trouble-code reply",
				"module", module,
				"mode", m.mode,
				"response", resp,
				"error", err)
			continue
		}

		answered = true
		for i := range codes {
			codes[i].Module = module
		}
		found = append(found, codes...)
	}

	if !answered {
		return nil, ErrNoData
	}
	return found, nil
}

// ClearDTCs erases stored codes via mode 04.
func (s *SerialOBD) ClearDTCs(ctx context.Context) error {
	elm, err := s.adapter()
	if err != nil {
		return err
	}

	if err := s.acquireBus(ctx); err != nil {
		return err
	}
	defer s.releaseBus()

	resp, err := elm.Query(ctx, "04")
	if err != nil {
		return fmt.Errorf("clear codes: %w", err)
	}
	if err := responseError(resp); err != nil {
		return fmt.Errorf("clear codes: %w", err)
	}

	// A positive reply to mode 04 is 0x44.
	data, err := normalizeHex(resp)
	if err != nil {
		return err
	}
	if _, ok := payload(data, 0x04); !ok {
		return fmt.Errorf("%w: mode 04 returned %q", ErrParse, resp)
	}

	slog.Info("Stored trouble codes cleared")
	return nil
}

// EnterLowPower puts the adapter to sleep to avoid draining the battery when
// left plugged in.
func (s *SerialOBD) EnterLowPower(ctx context.Context) error {
	elm, err := s.adapter()
	if err != nil {
		return err
	}
	if err := s.acquireBus(ctx); err != nil {
		return err
	}
	defer s.releaseBus()

	_, err = elm.Query(ctx, cmdLowPower)
	return err
}

// noteQueryFailure drops the connection when a failure looks like the adapter
// going away rather than the vehicle declining to answer. Timeouts and NO DATA
// are ordinary; an I/O error on the port is not.
func (s *SerialOBD) noteQueryFailure(err error) {
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, ErrNoData), errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrBadCommand),
		errors.Is(err, ErrBusy):
		return
	}

	s.mu.Lock()
	elm := s.elm
	s.elm = nil
	s.connected = false
	s.lastErr = err
	s.mu.Unlock()

	if elm != nil {
		elm.Close()
		slog.Warn("Adapter link lost, will retry", "error", err)
	}
}

// compile-time check that the provider satisfies the interface.
var _ obd.OBDProvider = (*SerialOBD)(nil)
