package obd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/tarm/serial"
)

// SerialOBD implements OBDProvider backed by a serial (ELM327-like) device.
// It attempts to (re)connect with exponential backoff and does light polling
// of RPM and coolant using OBD-II PIDs (010C for RPM, 0105 for coolant).
// This is a pragmatic, small implementation — production-ready code would
// need more robust parsing and support for multiple protocols.
type SerialOBD struct {
	mu        sync.RWMutex
	portName  string
	baud      int
	port      io.ReadWriteCloser
	running   bool
	rpm       int
	coolant   float64
	errors    []DTCEntry
	stopCh    chan struct{}
	connected bool
}

// NewSerialOBD creates a SerialOBD for the given device path (e.g., COM3 or /dev/ttyUSB0).
func NewSerialOBD() *SerialOBD {
	return &SerialOBD{
		portName: detectPlatformSerialDev(),
		baud:     9600, // Try lower baud rate
		stopCh:   make(chan struct{}),
		rpm:      0,
		coolant:  0,
		errors:   nil,
	}
}

func (s *SerialOBD) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	// spawn reconnect/poll goroutine
	go s.run(ctx)
	return nil
}

func (s *SerialOBD) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stopCh)
	if s.port != nil {
		s.port.Close()
	}
	s.running = false
	s.connected = false
	s.mu.Unlock()
}

func (s *SerialOBD) Connected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

func (s *SerialOBD) GetRPM() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.connected {
		return 0, errors.New("not connected")
	}
	return s.rpm, nil
}

func (s *SerialOBD) GetCoolantTemp() (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.connected {
		return 0, errors.New("not connected")
	}
	return s.coolant, nil
}

func (s *SerialOBD) GetTotalKilometers() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return 0, nil // Placeholder return
}

func (s *SerialOBD) GetOilTemp() (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return 0.0, nil // Placeholder return
}

func (s *SerialOBD) GetErrors() ([]DTCEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyErr := make([]DTCEntry, len(s.errors))
	copy(copyErr, s.errors)
	return copyErr, nil
}

func (s *SerialOBD) run(ctx context.Context) {
	// reconnect/backoff loop
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
			// try connect
			if err := s.tryConnect(); err != nil {
				// mark disconnected
				s.setConnected(false)
				// wait and backoff
				time.Sleep(backoff)
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
				continue
			}
			// connected
			backoff = time.Second
			s.setConnected(true)
			// polling loop while connected
			reader := bufio.NewReader(s.port)
			// run a first immediate read of values before entering the periodic ticker
			s.updateRPM(reader)
			s.updateCoolant(reader)
			s.updateDTCs(reader)
			pollTicker := time.NewTicker(5 * time.Second)
			for {
				select {
				case <-ctx.Done():
					pollTicker.Stop()
					return
				case <-s.stopCh:
					pollTicker.Stop()
					return
				case <-pollTicker.C:
					// update values (RPM, coolant, DTCs)
					s.updateRPM(reader)
					s.updateCoolant(reader)
					s.updateDTCs(reader)

				}
				// loop returns here on disconnect and we try reconnect
			}
		}
	}
}

func (s *SerialOBD) tryConnect() error {
	fmt.Printf("[Serial] Attempting to connect to port %s at %d baud\n", s.portName, s.baud)
	cfg := &serial.Config{
		Name:        s.portName,
		Baud:        s.baud,
		ReadTimeout: 2 * time.Second, // Increased timeout
		Size:        8,
		Parity:      serial.ParityNone,
		StopBits:    serial.Stop1,
	}
	p, err := serial.OpenPort(cfg)
	if err != nil {
		fmt.Printf("[Serial] Failed to open port: %v\n", err)
		return err
	}
	s.mu.Lock()
	s.port = p
	s.mu.Unlock()
	fmt.Printf("[Serial] Port opened successfully, initializing ELM327\n")
	// Initialize ELM327: send reset-like commands
	// Reset and wait for device to stabilize
	s.sendCommand("ATZ")
	time.Sleep(1 * time.Second)

	// Try to clear any garbage data
	reader := bufio.NewReader(s.port)
	for reader.Buffered() > 0 {
		_, _ = reader.ReadByte()
	}

	// Configure ELM327 settings
	s.sendCommand("ATD") // Set all to defaults
	time.Sleep(100 * time.Millisecond)
	s.sendCommand("ATL0") // Line feeds off
	time.Sleep(100 * time.Millisecond)
	s.sendCommand("ATE0") // Echo off
	time.Sleep(100 * time.Millisecond)
	s.sendCommand("ATH0") // Headers off
	time.Sleep(100 * time.Millisecond)
	s.sendCommand("ATS0") // Spaces off
	time.Sleep(100 * time.Millisecond)

	// Try auto protocol first
	s.sendCommand("ATSP0") // Auto protocol detection
	time.Sleep(200 * time.Millisecond)

	// Set timeout and try reading any response
	line, _ := readELMResponse(reader, 1*time.Second)
	fmt.Printf("[Serial] Protocol init response: %q\n", line)

	// Try each protocol explicitly if auto fails
	protocols := []string{"ATSP1", "ATSP2", "ATSP3", "ATSP4", "ATSP5", "ATSP6"}
	for _, proto := range protocols {
		s.sendCommand(proto)
		time.Sleep(200 * time.Millisecond)
		// Try a simple command to test protocol
		s.sendCommand("0100")
		time.Sleep(300 * time.Millisecond)
		line, _ := readELMResponse(reader, 1*time.Second)
		if strings.Contains(line, "41") { // Valid response starts with 41
			fmt.Printf("[Serial] Found working protocol with %s\n", proto)
			break
		}
		fmt.Printf("[Serial] Protocol %s response: %q\n", proto, line)
	}

	fmt.Printf("[Serial] ELM327 initialization completed\n")
	return nil
}

func (s *SerialOBD) sendCommand(cmd string) {
	if s.port == nil {
		fmt.Printf("[Serial] Cannot send command: port is nil\n")
		return
	}
	full := cmd + "\r"
	n, err := s.port.Write([]byte(full))
	if err != nil {
		fmt.Printf("[Serial] Error writing command %q: %v\n", cmd, err)
		return
	}
	fmt.Printf("[Serial] Successfully wrote %d bytes for command %q\n", n, cmd)
}

func (s *SerialOBD) setConnected(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = v
}

// parseELMResponseRPM tries to parse a minimal ELM327 response for 010C (RPM).
// Real responses look like: "41 0C 1A F8" (where RPM = ((A*256)+B)/4)
func parseELMResponseRPM(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	// crude split
	parts := strings.Fields(line)
	// find sequence 41 0C A B
	for i := 0; i+3 < len(parts); i++ {
		if strings.EqualFold(parts[i], "41") && strings.EqualFold(parts[i+1], "0C") {
			// parse A and B hex
			a, err1 := parseHexByte(parts[i+2])
			b, err2 := parseHexByte(parts[i+3])
			if err1 == nil && err2 == nil {
				rpm := ((int(a) * 256) + int(b)) / 4
				return rpm, true
			}
		}
	}
	return 0, false
}

func parseHexByte(s string) (byte, error) {
	var v byte
	_, err := fmt.Sscanf(s, "%02X", &v)
	if err != nil {
		_, err2 := fmt.Sscanf(s, "%02x", &v)
		if err2 != nil {
			return 0, err
		}
	}
	return v, nil
}

// parseELMResponseTemp parses 0105 (coolant) response: 41 05 A => temp = A - 40
func parseELMResponseTemp(line string) (float64, bool) {
	line = strings.TrimSpace(line)
	parts := strings.Fields(line)
	for i := 0; i+2 < len(parts); i++ {
		if strings.EqualFold(parts[i], "41") && strings.EqualFold(parts[i+1], "05") {
			a, err := parseHexByte(parts[i+2])
			if err == nil {
				return float64(int(a) - 40), true
			}
		}
	}
	return 0, false
}

// parseELMResponseDTCs parses a mode 03 / response 43 ELM327 reply into DTCEntry list.
// ELM/OBD responses typically include tokens like: "43 01 33 00 00" where each DTC is two bytes.
// Each DTC is encoded per SAE J2012: first byte high 2 bits select the letter (P/C/B/U),
// remaining nibbles form the 4-digit code.
func parseELMResponseDTCs(line string) ([]DTCEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		fmt.Printf("[DTC Parser] Empty response line\n")
		return nil, false
	}
	if strings.Contains(strings.ToUpper(line), "NO DATA") || strings.Contains(strings.ToUpper(line), "NODATA") {
		fmt.Printf("[DTC Parser] NO DATA response from device\n")
		return nil, false
	}
	parts := strings.Fields(line)
	fmt.Printf("[DTC Parser] Processing response tokens: %v\n", parts)
	var results []DTCEntry
	letters := []byte{'P', 'C', 'B', 'U'}

	// find all occurrences of 43 (response to mode 03)
	for i := 0; i < len(parts); i++ {
		if strings.EqualFold(parts[i], "43") {
			// follow pairs of bytes after this
			for j := i + 1; j+1 < len(parts); j += 2 {
				a, err1 := parseHexByte(parts[j])
				b, err2 := parseHexByte(parts[j+1])
				if err1 != nil || err2 != nil {
					// stop parsing this sequence on parse error
					break
				}
				// if both bytes are zero, no code
				if a == 0 && b == 0 {
					continue
				}
				letterIndex := (a & 0xC0) >> 6
				if int(letterIndex) > len(letters)-1 {
					// unexpected, skip
					continue
				}
				first := (a & 0x30) >> 4
				second := a & 0x0F
				third := (b & 0xF0) >> 4
				fourth := b & 0x0F
				code := fmt.Sprintf("%c%X%X%X%X", letters[letterIndex], first, second, third, fourth)
				results = append(results, DTCEntry{Code: code, Description: ""})
			}
		}
	}
	if len(results) == 0 {
		return nil, false
	}
	return results, true
}

// updateRPM sends the RPM PID and updates the internal rpm field if parse succeeds.
func (s *SerialOBD) updateRPM(reader *bufio.Reader) {
	s.sendCommand("010C")
	time.Sleep(50 * time.Millisecond)
	// read multi-line response until '>' prompt or timeout
	line, _ := readELMResponse(reader, 1200*time.Millisecond)
	if parsed, ok := parseELMResponseRPM(line); ok {
		s.mu.Lock()
		s.rpm = parsed
		s.mu.Unlock()
	}
}

// updateCoolant sends the coolant PID and updates the internal coolant field if parse succeeds.
func (s *SerialOBD) updateCoolant(reader *bufio.Reader) {
	s.sendCommand("0105")
	time.Sleep(50 * time.Millisecond)
	// read multi-line response until '>' prompt or timeout
	line, _ := readELMResponse(reader, 1200*time.Millisecond)
	if parsed, ok := parseELMResponseTemp(line); ok {
		s.mu.Lock()
		s.coolant = parsed
		s.mu.Unlock()
	}
}

// updateDTCs queries stored trouble codes (mode 03) and updates the internal errors slice.
func (s *SerialOBD) updateDTCs(reader *bufio.Reader) {
	fmt.Printf("[DTC] Sending Mode 03 command to query DTCs\n")

	// Try to clear any stale data by reading what's available
	for reader.Buffered() > 0 {
		_, _ = reader.ReadByte()
	}

	// Send command and wait longer for response
	s.sendCommand("03")
	time.Sleep(500 * time.Millisecond) // Increased delay to give more time for response

	// read multi-line response until '>' prompt or timeout
	line, err := readELMResponse(reader, 3*time.Second) // Increased timeout further
	fmt.Printf("[DTC] Raw response from ELM327: %q (err: %v)\n", line, err)
	if parsedErrs, ok := parseELMResponseDTCs(line); ok {
		s.mu.Lock()
		s.errors = parsedErrs
		fmt.Printf("[DTC] Successfully parsed %d trouble codes\n", len(parsedErrs))
		s.mu.Unlock()
	} else {
		fmt.Printf("[DTC] No trouble codes found or parsing failed\n")
	}
}

// readELMResponse collects bytes from the reader until the ELM327 prompt '>' is seen
// or the provided timeout elapses. It returns the concatenated response (prompt excluded).
// Note: underlying serial port has its own ReadTimeout; this function uses a goroutine
// to avoid blocking too long and returns what it collected on read errors or timeout.
func readELMResponse(reader *bufio.Reader, timeout time.Duration) (string, error) {
	fmt.Printf("[Serial] Starting to read response (timeout: %v)\n", timeout)
	var sb strings.Builder
	ch := make(chan byte)
	errCh := make(chan error, 1)

	go func() {
		readTimeout := time.After(timeout)
		for {
			select {
			case <-readTimeout:
				fmt.Printf("[Serial] Individual byte read timed out\n")
				errCh <- fmt.Errorf("byte read timeout")
				return
			default:
				b, err := reader.ReadByte()
				if err != nil {
					if err == io.EOF {
						fmt.Printf("[Serial] EOF while reading\n")
					} else {
						fmt.Printf("[Serial] Error reading byte: %v\n", err)
					}
					errCh <- err
					return
				}
				fmt.Printf("[Serial] Read byte: %02X (%c)\n", b, b)
				ch <- b
				if b == '>' {
					return
				}
			}
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case b := <-ch:
			if b == '>' {
				result := sb.String()
				fmt.Printf("[Serial] Complete response: %q\n", result)
				return result, nil
			}
			sb.WriteByte(b)
		case err := <-errCh:
			result := sb.String()
			fmt.Printf("[Serial] Response ended with error: %v. Partial response: %q\n", err, result)
			return result, err
		case <-timer.C:
			result := sb.String()
			fmt.Printf("[Serial] Response timed out. Partial response: %q\n", result)
			return result, nil
		}
	}
}
