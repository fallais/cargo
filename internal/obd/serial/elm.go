package serial

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/tarm/serial"
)

// AT commands. ELM327 configuration is all "AT" prefixed; anything else is
// passed through to the vehicle bus.
const (
	cmdReset          = "ATZ"
	cmdEchoOff        = "ATE0"
	cmdLineFeedsOff   = "ATL0"
	cmdHeadersOff     = "ATH0"
	cmdHeadersOn      = "ATH1"
	cmdSpacesOff      = "ATS0"
	cmdProtocolAuto   = "ATSP0"
	cmdProtocolNumber = "ATDPN"
	cmdReadVoltage    = "ATRV"
	cmdLowPower       = "ATLP"
	cmdDefaults       = "ATD"

	// terminator ends every command sent to the adapter.
	terminator = "\r"
	// prompt is what the adapter prints when it is ready for the next
	// command; it is the only reliable end-of-response marker.
	prompt = '>'
)

// defaultResponseTimeout is the adapter's power-on ATST value, restored after
// a slower module has been queried.
const defaultResponseTimeout byte = 0x32

// baudRates are tried in order when probing an adapter. 38400 and 9600 are the
// two an ELM327 ships with depending on the clone; the faster rates appear on
// newer boards.
var baudRates = []int{38400, 9600, 115200, 230400, 500000}

// ELM327 is the transport: it moves command strings to the adapter and
// response strings back. It knows nothing about what those strings mean, which
// keeps protocol handling separate from OBD semantics.
type ELM327 struct {
	mu       sync.Mutex
	port     *serial.Port
	reader   *bufio.Reader
	portName string
	baudRate int
	protocol string
	headers  bool
}

// Options configure a connection.
type Options struct {
	// Port is the device path. Empty means probe for one.
	Port string
	// Baud pins the rate. Zero means try the common rates in turn.
	Baud int
	// ReadTimeout bounds a single response.
	ReadTimeout time.Duration
}

func (o *Options) setDefaults() {
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = 5 * time.Second
	}
}

// ports returns the devices to try, in order.
func (o Options) ports() []string {
	if o.Port != "" {
		return []string{o.Port}
	}
	if found := listPlatformSerialDevs(); len(found) > 0 {
		return found
	}
	return nil
}

// ErrNoAdapter means no candidate device could be opened at all, as opposed to
// a device that opened but did not behave like an ELM327.
var ErrNoAdapter = errors.New("no serial adapter found")

// Open finds an adapter and initialises it.
//
// With no port configured it walks every device that could plausibly be one.
// That matters because the same adapter shows up as ttyUSB0, ttyACM0 or
// rfcomm0 depending on the chipset and how it is attached.
func Open(ctx context.Context, opts Options) (*ELM327, error) {
	opts.setDefaults()

	ports := opts.ports()
	if len(ports) == 0 {
		return nil, fmt.Errorf("%w: looked for %s", ErrNoAdapter, candidateDescription())
	}

	rates := baudRates
	if opts.Baud > 0 {
		rates = []int{opts.Baud}
	}

	var (
		opened      bool
		lastOpenErr error
	)
	for _, name := range ports {
		for _, baud := range rates {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			port, err := serial.OpenPort(&serial.Config{
				Name:        name,
				Baud:        baud,
				Size:        8,
				Parity:      serial.ParityNone,
				StopBits:    serial.Stop1,
				ReadTimeout: 100 * time.Millisecond,
			})
			if err != nil {
				// A device that is absent or busy fails the same way
				// at every rate, so move to the next device.
				slog.Debug("Port unavailable", "port", name, "error", err)
				lastOpenErr = err
				break
			}
			opened = true

			e := &ELM327{
				port:     port,
				reader:   bufio.NewReader(port),
				portName: name,
				baudRate: baud,
			}

			if err := e.initialise(ctx, opts.ReadTimeout); err != nil {
				slog.Debug("Not an ELM327 at this rate",
					"port", name, "baud", baud, "error", err)
				port.Close()
				continue
			}

			slog.Info("Adapter ready",
				"port", e.portName,
				"baud", e.baudRate,
				"protocol", e.ProtocolName())
			return e, nil
		}
	}

	if !opened {
		// Report why the device could not be opened, not just that it was
		// not. A permission error looks identical to an absent adapter
		// from the outside, and on Linux it is the more likely of the two
		// once a device node exists at all.
		if lastOpenErr != nil {
			if errors.Is(lastOpenErr, fs.ErrPermission) {
				return nil, fmt.Errorf("%w: %v (the device exists but is not readable; "+
					"on Linux add yourself to the dialout group and log back in)",
					ErrNoAdapter, lastOpenErr)
			}
			return nil, fmt.Errorf("%w: %v", ErrNoAdapter, lastOpenErr)
		}
		return nil, fmt.Errorf("%w: tried %s", ErrNoAdapter, strings.Join(ports, ", "))
	}
	return nil, fmt.Errorf("a device opened but did not answer as an ELM327 (tried %s)",
		strings.Join(ports, ", "))
}

// initialise resets the adapter and puts it in a known output format.
func (e *ELM327) initialise(ctx context.Context, timeout time.Duration) error {
	e.port.Flush()

	// ATZ reboots the adapter, which takes noticeably longer than any other
	// command and answers with its version banner.
	resp, err := e.query(ctx, cmdReset, 3*time.Second)
	if err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	if !strings.Contains(strings.ToUpper(resp), "ELM") {
		return fmt.Errorf("%w: reset returned %q, not a version banner", ErrParse, resp)
	}

	// Echo off first: until it is, every reply is prefixed with the command
	// that produced it and nothing else parses cleanly.
	for _, cmd := range []string{cmdEchoOff, cmdLineFeedsOff, cmdSpacesOff, cmdHeadersOff} {
		if _, err := e.query(ctx, cmd, timeout); err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
	}
	e.headers = false

	// A reading well under battery voltage means the adapter is running off
	// the USB side with no vehicle attached, which is worth saying plainly
	// rather than letting every later query time out.
	if resp, err := e.query(ctx, cmdReadVoltage, timeout); err == nil {
		if v, err := ParseVoltage(resp); err == nil {
			slog.Info("Adapter supply voltage", "volts", v)
			if v < 6.0 {
				return fmt.Errorf("%w: %.1fV", ErrLowVoltage, v)
			}
		}
	}

	if _, err := e.query(ctx, cmdProtocolAuto, timeout); err != nil {
		return fmt.Errorf("%s: %w", cmdProtocolAuto, err)
	}
	return nil
}

// Negotiate makes the adapter settle on a bus protocol and reports which one.
//
// ATSP0 only arms automatic detection; the adapter does not actually probe
// until a vehicle request is sent, so mode 01 PID 00 is used as the trigger.
func (e *ELM327) Negotiate(ctx context.Context) error {
	if _, err := e.Query(ctx, "0100"); err != nil {
		return fmt.Errorf("no vehicle bus: %w", err)
	}

	resp, err := e.Query(ctx, cmdProtocolNumber)
	if err != nil {
		return fmt.Errorf("read protocol number: %w", err)
	}

	e.mu.Lock()
	// A leading "A" means the number was reached automatically.
	e.protocol = strings.TrimPrefix(strings.TrimSpace(resp), "A")
	e.mu.Unlock()

	slog.Info("Bus protocol negotiated", "protocol", e.ProtocolName())
	return nil
}

var protocolNames = map[string]string{
	"0": "Automatic",
	"1": "SAE J1850 PWM (41.6 kbaud)",
	"2": "SAE J1850 VPW (10.4 kbaud)",
	"3": "ISO 9141-2 (5 baud init)",
	"4": "ISO 14230-4 KWP (5 baud init)",
	"5": "ISO 14230-4 KWP (fast init)",
	"6": "ISO 15765-4 CAN (11 bit, 500 kbaud)",
	"7": "ISO 15765-4 CAN (29 bit, 500 kbaud)",
	"8": "ISO 15765-4 CAN (11 bit, 250 kbaud)",
	"9": "ISO 15765-4 CAN (29 bit, 250 kbaud)",
	"A": "SAE J1939 CAN (29 bit, 250 kbaud)",
}

// ProtocolName returns the negotiated protocol in readable form.
func (e *ELM327) ProtocolName() string {
	e.mu.Lock()
	p := e.protocol
	e.mu.Unlock()

	if name, ok := protocolNames[strings.ToUpper(p)]; ok {
		return name
	}
	if p == "" {
		return "not negotiated"
	}
	return "unknown (" + p + ")"
}

// IsCAN reports whether the negotiated protocol is one of the CAN variants,
// which differ from the older buses in how replies are framed.
func (e *ELM327) IsCAN() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch strings.ToUpper(e.protocol) {
	case "6", "7", "8", "9", "A":
		return true
	}
	return false
}

// SetHeader points subsequent requests at one ECU instead of broadcasting.
func (e *ELM327) SetHeader(ctx context.Context, addr uint16) error {
	_, err := e.Query(ctx, fmt.Sprintf("ATSH%03X", addr))
	return err
}

// SetReceiveFilter restricts which replies are shown.
//
// Without it a physically addressed request still surfaces chatter from other
// ECUs, and with headers suppressed there is no way to tell whose reply is
// whose - so codes get attributed to the wrong module.
func (e *ELM327) SetReceiveFilter(ctx context.Context, addr uint16) error {
	_, err := e.Query(ctx, fmt.Sprintf("ATCRA%03X", addr))
	return err
}

// SetFlowControl tells the adapter how to acknowledge a multi-frame reply.
//
// This is the step that makes UDS work on a non-standard address. A reply
// longer than one CAN frame requires the tester to send a flow-control frame,
// and the adapter builds that from its own defaults, which assume the
// emissions addresses. Pointed at an ABS module those defaults are wrong, the
// ECU never receives permission to continue, and the transfer stalls after the
// first frame with no error to show for it.
//
// The data bytes are the standard "continue, unlimited block size, no minimum
// separation" response.
func (e *ELM327) SetFlowControl(ctx context.Context, requestID uint16) error {
	for _, cmd := range []string{
		fmt.Sprintf("ATFCSH%03X", requestID),
		"ATFCSD300000",
		"ATFCSM1", // use the header and data set above
	} {
		if _, err := e.Query(ctx, cmd); err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
	}
	return nil
}

// ClearFlowControl returns the adapter to building flow control itself.
func (e *ELM327) ClearFlowControl(ctx context.Context) error {
	_, err := e.Query(ctx, "ATFCSM0")
	return err
}

// SetResponseTimeout sets how long the adapter waits for a reply, in units of
// roughly four milliseconds.
//
// The default is tuned for OBD-II, where answers are immediate. A body module
// gathering fault records can take far longer, and the adapter would otherwise
// give up and report no data while the ECU was still composing its answer.
func (e *ELM327) SetResponseTimeout(ctx context.Context, units byte) error {
	_, err := e.Query(ctx, fmt.Sprintf("ATST%02X", units))
	return err
}

// ClearReceiveFilter restores the default acceptance of all replies.
func (e *ELM327) ClearReceiveFilter(ctx context.Context) error {
	_, err := e.Query(ctx, "ATCRA")
	return err
}

// SetHeaders shows or hides the sender address on each reply.
func (e *ELM327) SetHeaders(ctx context.Context, on bool) error {
	cmd := cmdHeadersOff
	if on {
		cmd = cmdHeadersOn
	}
	if _, err := e.Query(ctx, cmd); err != nil {
		return err
	}

	e.mu.Lock()
	e.headers = on
	e.mu.Unlock()
	return nil
}

// Voltage reads the adapter's view of the battery.
func (e *ELM327) Voltage(ctx context.Context) (float64, error) {
	resp, err := e.Query(ctx, cmdReadVoltage)
	if err != nil {
		return 0, err
	}
	return ParseVoltage(resp)
}

// Query sends a command and returns the adapter's reply.
//
// It serialises access: the adapter is a single half-duplex conversation, so
// two callers interleaving commands would each read the other's answer.
func (e *ELM327) Query(ctx context.Context, cmd string) (string, error) {
	return e.query(ctx, cmd, 5*time.Second)
}

func (e *ELM327) query(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.port == nil {
		return "", ErrNotConnected
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Drop anything left over from a previous command that timed out, so a
	// stale reply is not read as the answer to this one.
	e.drain()

	if _, err := e.port.Write([]byte(cmd + terminator)); err != nil {
		return "", fmt.Errorf("write %q: %w", cmd, err)
	}

	resp, err := e.readUntilPrompt(ctx, timeout)
	if err != nil {
		return resp, fmt.Errorf("%q: %w", cmd, err)
	}

	slog.Debug("Adapter exchange", "command", cmd, "response", resp)
	return resp, nil
}

// drain discards buffered input. The port read timeout makes this terminate
// once the adapter has nothing left to say.
func (e *ELM327) drain() {
	for e.reader.Buffered() > 0 {
		if _, err := e.reader.Discard(e.reader.Buffered()); err != nil {
			return
		}
	}
}

// readUntilPrompt collects bytes until the adapter's '>' or the deadline.
//
// The underlying port read timeout is deliberately short so this loop keeps
// control: it can then honour both the caller's context and its own deadline,
// rather than blocking for however long the driver decides.
func (e *ELM327) readUntilPrompt(ctx context.Context, timeout time.Duration) (string, error) {
	var sb strings.Builder
	deadline := time.Now().Add(timeout)

	for {
		if err := ctx.Err(); err != nil {
			return strings.TrimSpace(sb.String()), err
		}
		if time.Now().After(deadline) {
			return strings.TrimSpace(sb.String()), fmt.Errorf("%w after %v", ErrTimeout, timeout)
		}

		b, err := e.reader.ReadByte()
		if err != nil {
			// A read timeout on an idle port is normal; keep waiting
			// until our own deadline decides otherwise.
			continue
		}

		if b == prompt {
			return strings.TrimSpace(sb.String()), nil
		}
		// Keep printable characters and line breaks; the adapter emits
		// stray NULs and control bytes that would corrupt parsing.
		if b == '\r' || b == '\n' || (b >= 0x20 && b <= 0x7E) {
			sb.WriteByte(b)
		}
	}
}

// Close releases the port.
func (e *ELM327) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.port == nil {
		return nil
	}
	port := e.port
	e.port = nil
	e.reader = nil
	return port.Close()
}
