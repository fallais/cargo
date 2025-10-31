package serial

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
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
	CommandLowPower        = "ATLP"
	CommandProtocolNum     = "ATDPN"
	CommandReadVoltage     = "ATRV"

	CR = "\r"

	// Supported protocol IDs
	ProtocolAuto          = "0" // Automatic mode
	ProtocolJ1850PWM      = "1" // SAE J1850 PWM
	ProtocolJ1850VPW      = "2" // SAE J1850 VPW
	ProtocolISO9141       = "3" // ISO 9141-2
	ProtocolISO14230_5    = "4" // ISO 14230-4 (KWP 5BAUD)
	ProtocolISO14230      = "5" // ISO 14230-4 (KWP FAST)
	ProtocolISO15765_11   = "6" // ISO 15765-4 (CAN 11/500)
	ProtocolISO15765_29   = "7" // ISO 15765-4 (CAN 29/500)
	ProtocolISO15765_11_2 = "8" // ISO 15765-4 (CAN 11/250)
	ProtocolISO15765_29_2 = "9" // ISO 15765-4 (CAN 29/250)
	ProtocolSAEJ1939      = "A" // SAE J1939 (CAN 29/250)
)

// SerialOBD implements OBDProvider backed by a serial (ELM327-like) device.
type SerialOBD struct {
	portName    string
	baud        int
	port        io.ReadWriteCloser
	stopCh      chan struct{}
	isLowPower  bool
	isConnected bool

	mu sync.RWMutex
}

// NewSerialOBD creates a SerialOBD.
func New(baud int) obd.OBDProvider {
	return &SerialOBD{
		portName: detectPlatformSerialDev(),
		baud:     baud,
		stopCh:   make(chan struct{}),
	}
}

func (s *SerialOBD) Start(ctx context.Context) error {
	// Connect
	err := s.open()
	if err != nil {
		return fmt.Errorf("error while connecting: %v", err)
	}

	// Try to detect protocol automatically first
	if err := s.autoDetectProtocol(); err != nil {
		log.Warn("Auto protocol detection failed, will try specific protocols", zap.Error(err))
		// Try specific protocols in order of likelihood
		protocols := []string{
			ProtocolISO15765_11,   // ISO 15765-4 (CAN 11/500) - Most common
			ProtocolISO15765_11_2, // ISO 15765-4 (CAN 11/250)
			ProtocolJ1850PWM,      // SAE J1850 PWM
			ProtocolISO9141,       // ISO 9141-2
			ProtocolISO14230,      // ISO 14230-4 (KWP FAST)
		}

		for _, protocol := range protocols {
			if err := s.tryProtocol(protocol); err == nil {
				log.Info("Successfully connected using protocol", zap.String("protocol", protocol))
				break
			}
		}
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

// EnterLowPower puts the ELM327 into low power mode
func (s *SerialOBD) EnterLowPower() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isConnected {
		return fmt.Errorf("cannot enter low power when not connected")
	}

	if err := s.sendCommand(CommandLowPower); err != nil {
		return fmt.Errorf("failed to enter low power mode: %v", err)
	}

	// Wait a bit for the command to take effect
	time.Sleep(100 * time.Millisecond)

	reader := bufio.NewReader(s.port)
	resp, err := readELMResponse(reader, 500*time.Millisecond)
	if err != nil || !strings.Contains(resp, "OK") {
		return fmt.Errorf("did not receive OK response for low power mode")
	}

	s.isLowPower = true
	log.Info("Successfully entered low power mode")
	return nil
}

// ExitLowPower wakes up the ELM327 from low power mode
func (s *SerialOBD) ExitLowPower() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isConnected {
		return fmt.Errorf("cannot exit low power when not connected")
	}

	// Send a space to wake up the device
	if _, err := s.port.Write([]byte(" ")); err != nil {
		return fmt.Errorf("failed to send wake up command: %v", err)
	}

	// Wait for the device to wake up
	time.Sleep(1 * time.Second)

	s.isLowPower = false
	log.Info("Successfully exited low power mode")
	return nil
}

// autoDetectProtocol attempts to automatically detect the protocol using ATSP0
func (s *SerialOBD) autoDetectProtocol() error {
	// First try automatic protocol detection
	if err := s.sendCommand(CommandSetProtocolAuto); err != nil {
		return fmt.Errorf("failed to set auto protocol: %v", err)
	}

	// Try a basic command to see if we can communicate
	if err := s.sendCommand("0100"); err != nil {
		return fmt.Errorf("failed to send test command: %v", err)
	}

	// Get current protocol number
	if err := s.sendCommand(CommandProtocolNum); err != nil {
		return fmt.Errorf("failed to get protocol number: %v", err)
	}

	reader := bufio.NewReader(s.port)
	resp, err := readELMResponse(reader, 1*time.Second)
	if err != nil || resp == "" {
		return fmt.Errorf("no response from protocol query")
	}

	// Response might be prefixed with "A" for auto
	protocolNum := strings.TrimPrefix(resp, "A")
	log.Info("Detected protocol", zap.String("protocol", protocolNum))
	return nil
}

// tryProtocol attempts to connect using a specific protocol
func (s *SerialOBD) tryProtocol(protocol string) error {
	cmd := fmt.Sprintf("ATTP%s", protocol)
	if err := s.sendCommand(cmd); err != nil {
		return fmt.Errorf("failed to set protocol %s: %v", protocol, err)
	}

	// Try a basic command to test communication
	if err := s.sendCommand("0100"); err != nil {
		return fmt.Errorf("failed to communicate with protocol %s: %v", protocol, err)
	}

	reader := bufio.NewReader(s.port)
	resp, err := readELMResponse(reader, 1*time.Second)
	if err != nil || resp == "" {
		return fmt.Errorf("no response with protocol %s", protocol)
	}

	if strings.Contains(strings.ToUpper(resp), "UNABLE TO CONNECT") {
		return fmt.Errorf("unable to connect with protocol %s", protocol)
	}

	return nil
}

func (s *SerialOBD) GetErrors() ([]models.DTCEntry, error) {
	fmt.Printf("[DTC] Sending Mode 03 command to query DTCs\n")

	reader := bufio.NewReader(s.port)
	// Send command and wait longer for response
	s.sendCommand("03")
	time.Sleep(500 * time.Millisecond) // Increased delay to give more time for response

	// read multi-line response until '>' prompt or timeout
	line, err := readELMResponse(reader, 10*time.Second) // Increased timeout further
	fmt.Printf("[DTC] Raw response from ELM327: %q (err: %v)\n", line, err)
	if parsedErrs, ok := parseELMResponseDTCs(line); ok {
		return parsedErrs, nil
	} else {
		return nil, fmt.Errorf("[DTC] No trouble codes found or parsing failed")
	}
}

func (s *SerialOBD) open() error {
	fmt.Printf("[Serial] Attempting to connect to port %s at %d baud\n", s.portName, s.baud)
	cfg := &serial.Config{
		Name:        s.portName,
		Baud:        s.baud,
		ReadTimeout: 5 * time.Second, // Increased timeout for slow responding devices
		Size:        8,
		Parity:      serial.ParityNone,
		StopBits:    serial.Stop1,
	}

	// Try opening the port with retries
	var p *serial.Port
	var err error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		p, err = serial.OpenPort(cfg)
		if err == nil {
			break
		}
		log.Warn("Failed to open port, retrying...", zap.Error(err), zap.Int("attempt", i+1))
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return fmt.Errorf("failed to open port after %d attempts: %v", maxRetries, err)
	}

	// Try to flush any pending data
	if flusher, ok := p.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			log.Warn("Failed to flush port", zap.Error(err))
		}
	}

	// wait for port to stabilize - ELM327 needs more time
	time.Sleep(5 * time.Second)

	s.mu.Lock()
	s.port = p
	s.mu.Unlock()
	log.Info("[Serial] Port opened successfully", zap.String("config", fmt.Sprintf("%+v", cfg)))

	log.Info("Initializing ELM327 device over serial", zap.String("port", s.portName), zap.Int("baud", s.baud))
	s.initELM327()
	log.Info("ELM327 initialization completed")

	return nil
}

func (s *SerialOBD) initELM327() error {
	// Create a new reader for initialization
	reader := bufio.NewReader(s.port)

	// Reset and wait for response - with retries
	fmt.Printf("[ELM Init] Sending reset command...\n")
	maxRetries := 3
	var resetSuccess bool

	for i := 0; i < maxRetries; i++ {
		if err := s.sendCommand(CommandReset); err != nil {
			log.Warn("Reset attempt failed", zap.Int("attempt", i+1), zap.Error(err))
			time.Sleep(1 * time.Second)
			continue
		}

		// Wait longer after reset
		time.Sleep(3 * time.Second)

		// Check for response with longer timeout
		if resp, err := readELMResponse(reader, 2*time.Second); err == nil && resp != "" {
			log.Info("Reset successful", zap.String("response", resp))
			resetSuccess = true
			break
		}

		log.Warn("No response after reset attempt", zap.Int("attempt", i+1))
	}

	if !resetSuccess {
		return fmt.Errorf("device failed to respond after %d reset attempts", maxRetries)
	}

	// Send each command and verify response
	commands := []string{
		CommandEchoOff,         // Echo off
		CommandLineFeedsOff,    // Line feeds off
		CommandHeadersOff,      // Headers on (for better debugging)
		CommandSpacesOff,       // Spaces off
		"ATRV",                 // Read voltage
		CommandSetProtocolAuto, // Set protocol to automatic
	}

	for _, cmd := range commands {
		fmt.Printf("[ELM Init] Sending command: %q\n", cmd)
		if err := s.sendCommand(cmd); err != nil {
			return fmt.Errorf("command %s failed: %v", cmd, err)
		}

		resp, err := readELMResponse(reader, 500*time.Millisecond)
		fmt.Printf("[ELM Init] Command %q response: %q (err: %v)\n", cmd, resp, err)

		if cmd == "ATRV" && err == nil && resp != "" {
			// Try to parse voltage response
			if v, err := parseVoltage(resp); err == nil {
				if v < 6.0 {
					return fmt.Errorf("voltage too low: %.1fV", v)
				}
				fmt.Printf("[ELM Init] Voltage: %.1fV\n", v)
			}
		}

		if resp == "" && cmd != "ATRV" { // ATRV may not be supported on all devices
			return fmt.Errorf("command %s got no response", cmd)
		}

		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

func (s *SerialOBD) sendCommand(cmd string) error {
	if s.port == nil {
		return fmt.Errorf("cannot send command: port is nil")
	}

	// Clear any pending data before sending new command
	buffer := make([]byte, 1024)
	for {
		n, err := s.port.Read(buffer)
		if err != nil || n == 0 {
			break
		}
		log.Debug("Cleared pending data", zap.Int("bytes", n), zap.String("data", string(buffer[:n])))
	}

	full := cmd + CR

	// Write with retry
	maxRetries := 3
	var writeErr error

	for i := 0; i < maxRetries; i++ {
		n, err := s.port.Write([]byte(full))
		if err != nil {
			writeErr = err
			log.Warn("Write failed, retrying...",
				zap.String("command", cmd),
				zap.Error(err),
				zap.Int("attempt", i+1))
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if n != len(full) {
			writeErr = fmt.Errorf("incomplete write: %d/%d bytes", n, len(full))
			continue
		}
		writeErr = nil
		break
	}

	if writeErr != nil {
		return fmt.Errorf("[Serial] Error writing command %q after retries: %v", cmd, writeErr)
	}

	log.Debug("Command sent successfully",
		zap.String("command", cmd),
		zap.Int("bytes", len(full)))

	// Wait longer after write
	time.Sleep(200 * time.Millisecond)
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
// parseVoltage attempts to parse an ELM voltage response like "12.5V" into a float
func parseVoltage(response string) (float64, error) {
	// Remove any trailing V and whitespace
	response = strings.TrimSpace(strings.TrimSuffix(strings.ToUpper(response), "V"))
	return strconv.ParseFloat(response, 64)
}

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

// readELMResponse collects bytes from the reader until the ELM327 prompt '>' is seen
// or the provided timeout elapses. It returns the concatenated response (prompt excluded).
// Note: underlying serial port has its own ReadTimeout; this function uses a goroutine
// to avoid blocking too long and returns what it collected on read errors or timeout.
func readELMResponse(reader *bufio.Reader, timeout time.Duration) (string, error) {
	log.Debug("Starting to read response", zap.Duration("timeout", timeout))

	var sb strings.Builder
	readComplete := make(chan struct{})
	resultCh := make(chan string)
	errCh := make(chan error, 1)

	go func() {
		defer close(readComplete)
		buffer := make([]byte, 1)
		readDeadline := time.Now().Add(timeout)

		for time.Now().Before(readDeadline) {
			n, err := reader.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Debug("Read error", zap.Error(err))
					errCh <- err
					return
				}
				// On EOF, check if we have a complete response
				if sb.Len() > 0 {
					resultCh <- sb.String()
					return
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}

			if n == 0 {
				time.Sleep(10 * time.Millisecond)
				continue
			}

			b := buffer[0]
			log.Debug("Read byte",
				zap.String("hex", fmt.Sprintf("%02X", b)),
				zap.String("char", string(b)))

			if b == '>' {
				resultCh <- sb.String()
				return
			}

			// Filter out null bytes and other control characters except CR/LF
			if b >= 32 && b <= 126 || b == '\r' || b == '\n' {
				sb.WriteByte(b)
			}
		}

		// If we get here, the read loop timed out
		if sb.Len() > 0 {
			resultCh <- sb.String()
		} else {
			errCh <- fmt.Errorf("read timeout after %v", timeout)
		}
	}()

	select {
	case result := <-resultCh:
		log.Debug("Complete response received", zap.String("response", result))
		return strings.TrimSpace(result), nil
	case err := <-errCh:
		result := sb.String()
		log.Warn("Response ended with error",
			zap.Error(err),
			zap.String("partial_response", result))
		return strings.TrimSpace(result), err
	}
}
