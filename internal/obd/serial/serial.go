package serial

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"cargo/internal/models"
	"cargo/internal/obd"
	"cargo/pkg/log"

	"github.com/tarm/serial"
	"go.uber.org/zap"
)

const DefaultDelay = 100 * time.Millisecond

const (
	CommandReset           = "ATZ"
	CommandEchoOff         = "ATE0"
	CommandLineFeedsOff    = "ATL0"
	CommandHeadersOff      = "ATH1"
	CommandSpacesOff       = "ATS0"
	CommandSetProtocolAuto = "ATSP0"

	CR = "\r"
)

// SerialOBD implements OBDProvider backed by a serial (ELM327-like) device.
type SerialOBD struct {
	portName string
	baud     int
	port     io.ReadWriteCloser
	errors   []models.DTCEntry
	stopCh   chan struct{}

	isConnected bool

	mu sync.RWMutex
}

// NewSerialOBD creates a SerialOBD.
func New(baud int) obd.OBDProvider {
	return &SerialOBD{
		portName: detectPlatformSerialDev(),
		baud:     baud,
		stopCh:   make(chan struct{}),
		errors:   nil,
	}
}

func (s *SerialOBD) Start(ctx context.Context) error {
	// Connect
	err := s.open()
	if err != nil {
		return fmt.Errorf("error while connecting: %v", err)
	}
	s.setConnected(true)

	return nil
}

func (s *SerialOBD) Stop() {
	s.mu.Lock()
	close(s.stopCh)
	if s.port != nil {
		s.port.Close()
	}
	s.isConnected = false
	s.mu.Unlock()
}

func (s *SerialOBD) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isConnected
}

func (s *SerialOBD) GetRPM() (int, error) {
	if err := s.sendCommand(obd.PIDEngineRPM.String()); err != nil {
		return 0, fmt.Errorf("failed to send RPM command: %v", err)
	}
	time.Sleep(DefaultDelay)

	reader := bufio.NewReader(s.port)
	line, err := readELMResponse(reader, 1200*time.Millisecond)
	if err != nil {
		return 0, fmt.Errorf("failed to read RPM response: %v", err)
	}

	if rpm, ok := parseELMResponseRPM(line); ok {
		return rpm, nil
	}
	return 0, fmt.Errorf("failed to parse RPM response")
}

func (s *SerialOBD) GetCoolantTemp() (float64, error) {
	s.mu.RLock()
	if !s.isConnected {
		s.mu.RUnlock()
		return 0, errors.New("not connected")
	}
	s.mu.RUnlock()

	if err := s.sendCommand(obd.PIDCoolantTemp.String()); err != nil {
		return 0, fmt.Errorf("failed to send coolant temp command: %v", err)
	}
	time.Sleep(DefaultDelay)

	reader := bufio.NewReader(s.port)
	line, err := readELMResponse(reader, 1200*time.Millisecond)
	if err != nil {
		return 0, fmt.Errorf("failed to read coolant temp response: %v", err)
	}

	if temp, ok := parseELMResponseTemp(line); ok {
		return temp, nil
	}
	return 0, fmt.Errorf("failed to parse coolant temp response")
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

func (s *SerialOBD) GetErrors() ([]models.DTCEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	copyErr := make([]models.DTCEntry, len(s.errors))
	copy(copyErr, s.errors)

	return copyErr, nil
}

func (s *SerialOBD) open() error {
	fmt.Printf("[Serial] Attempting to connect to port %s at %d baud\n", s.portName, s.baud)
	cfg := &serial.Config{
		Name:        s.portName,
		Baud:        s.baud,
		ReadTimeout: 1 * time.Second,
		Size:        8,
		Parity:      serial.ParityNone,
		StopBits:    serial.Stop1,
	}
	p, err := serial.OpenPort(cfg)
	if err != nil {
		fmt.Printf("[Serial] Failed to open port: %v\n", err)
		return err
	}

	// wait for port to stabilize
	time.Sleep(2 * time.Second)

	s.mu.Lock()
	s.port = p
	s.mu.Unlock()
	log.Info("[Serial] Port opened successfully")

	log.Info("Initializing ELM327 device over serial", zap.String("port", s.portName), zap.Int("baud", s.baud))
	s.initELM327()
	log.Info("ELM327 initialization completed")

	return nil
}

func (s *SerialOBD) initELM327() {
	s.sendCommand(CommandReset)           // Reset
	s.sendCommand(CommandEchoOff)         // Echo off
	s.sendCommand(CommandLineFeedsOff)    // Line feeds off
	s.sendCommand(CommandHeadersOff)      // Headers off
	s.sendCommand(CommandSpacesOff)       // Spaces off
	s.sendCommand(CommandSetProtocolAuto) // Set protocol to automatic
}

func (s *SerialOBD) sendCommand(cmd string) error {
	if s.port == nil {
		return fmt.Errorf("cannot send command: port is nil")
	}

	full := cmd + CR

	n, err := s.port.Write([]byte(full))
	if err != nil {
		return fmt.Errorf("[Serial] Error writing command %q: %v", cmd, err)
	}
	fmt.Printf("[Serial] Successfully wrote %d bytes for command %q\n", n, cmd)

	time.Sleep(100 * time.Millisecond)
	return nil
}

func (s *SerialOBD) setConnected(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isConnected = v
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
func parseELMResponseDTCs(line string) ([]models.DTCEntry, bool) {
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
	var results []models.DTCEntry
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
				results = append(results, models.DTCEntry{Code: code, Description: ""})
			}
		}
	}
	if len(results) == 0 {
		return nil, false
	}
	return results, true
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
