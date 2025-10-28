package obd

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// MockOBD is a simple mock implementation of OBDProvider used for demo and testing.
type MockOBD struct {
	mu      sync.RWMutex
	running bool
	// simulated values
	rpm          int
	coolant      float64
	errors       []DTCEntry
	updateTicker *time.Ticker
	stopCh       chan struct{}
}

func NewMockOBD() *MockOBD {
	m := &MockOBD{
		rpm:     800,
		coolant: 75.0,
		errors:  []DTCEntry{},
		stopCh:  make(chan struct{}),
	}
	return m
}

func (m *MockOBD) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}
	m.updateTicker = time.NewTicker(1 * time.Second)
	m.running = true
	go func() {
		for {
			select {
			case <-m.updateTicker.C:
				m.mu.Lock()
				// random walk rpm and coolant
				m.rpm += rand.Intn(201) - 100
				if m.rpm < 600 {
					m.rpm = 600
				}
				if m.rpm > 4000 {
					m.rpm = 4000
				}
				m.coolant += float64(rand.Intn(21)-10) * 0.1
				if m.coolant < 60 {
					m.coolant = 60
				}
				if m.coolant > 110 {
					m.coolant = 110
				}
				// randomly add/remove an error
				if rand.Float32() < 0.05 {
					m.errors = append(m.errors, DTCEntry{Code: fmt.Sprintf("P%04d", rand.Intn(9999)), Description: "Random simulated fault"})
				}
				if len(m.errors) > 0 && rand.Float32() < 0.02 {
					m.errors = m.errors[1:]
				}
				m.mu.Unlock()
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			}
		}
	}()
	return nil
}

func (m *MockOBD) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return
	}
	m.updateTicker.Stop()
	close(m.stopCh)
	m.running = false
}

func (m *MockOBD) GetRPM() (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rpm, nil
}

func (m *MockOBD) GetCoolantTemp() (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.coolant, nil
}

func (m *MockOBD) GetTotalKilometers() (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return 0, nil // Placeholder return
}

func (m *MockOBD) GetOilTemp() (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return 0.0, nil // Placeholder return
}

func (m *MockOBD) GetErrors() ([]DTCEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	copyErr := make([]DTCEntry, len(m.errors))
	copy(copyErr, m.errors)
	return copyErr, nil
}

// Connected for MockOBD always returns true while running.
func (m *MockOBD) Connected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}
