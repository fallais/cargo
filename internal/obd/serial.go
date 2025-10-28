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
		baud:     38400,
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
			pollTicker := time.NewTicker(1 * time.Second)
			for {
				select {
				case <-ctx.Done():
					pollTicker.Stop()
					return
				case <-s.stopCh:
					pollTicker.Stop()
					return
				case <-pollTicker.C:
					// send RPM PID
					s.sendCommand("010C")
					time.Sleep(50 * time.Millisecond)
					line, _ := reader.ReadString('\r')
					if parsed, ok := parseELMResponseRPM(line); ok {
						s.mu.Lock()
						s.rpm = parsed
						s.mu.Unlock()
					}
					// send coolant PID
					s.sendCommand("0105")
					time.Sleep(50 * time.Millisecond)
					line2, _ := reader.ReadString('\r')
					if parsedC, ok := parseELMResponseTemp(line2); ok {
						s.mu.Lock()
						s.coolant = parsedC
						s.mu.Unlock()
					}
					// TODO: query errors (03) and parse
				}
				// loop returns here on disconnect and we try reconnect
			}
		}
	}
}

func (s *SerialOBD) tryConnect() error {
	cfg := &serial.Config{Name: s.portName, Baud: s.baud, ReadTimeout: time.Second}
	p, err := serial.OpenPort(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.port = p
	s.mu.Unlock()
	// Initialize ELM327: send reset-like commands
	s.sendCommand("ATZ")
	time.Sleep(200 * time.Millisecond)
	s.sendCommand("ATE0") // echo off
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (s *SerialOBD) sendCommand(cmd string) {
	if s.port == nil {
		return
	}
	full := cmd + "\r"
	s.port.Write([]byte(full))
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
