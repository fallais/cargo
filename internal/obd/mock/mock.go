// Package mock provides a simulated vehicle so the UI can be developed and
// demonstrated without an adapter or a car.
package mock

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"cargo/internal/dtc"
	"cargo/internal/obd"
)

// MockOBD simulates a vehicle with several controllers, so the module
// attribution in the UI has something to show.
type MockOBD struct {
	mu      sync.RWMutex
	running bool
	cancel  context.CancelFunc

	rpm       int
	coolant   float64
	oil       float64
	kilometre int
	codes     []dtc.DTC
}

// New builds a simulated vehicle with a plausible set of faults across three
// modules and all three code statuses.
func New() *MockOBD {
	return &MockOBD{
		rpm:       800,
		coolant:   75.0,
		oil:       90.0,
		kilometre: 142_338,
		codes:     seedCodes(),
	}
}

func seedCodes() []dtc.DTC {
	seed := []struct {
		code   string
		module string
		status dtc.Status
	}{
		{"P0301", "Engine", dtc.Stored},
		{"P0420", "Engine", dtc.Stored},
		{"P0133", "Engine", dtc.Pending},
		{"P0455", "Engine", dtc.Permanent},
		{"P0740", "Transmission", dtc.Stored},
		{"C1A15", "TPMS", dtc.Stored},
		// Vendor space and in no catalog: exercises the fallback that
		// describes a code from its encoding alone.
		{"P1234", "Engine", dtc.Stored},
	}

	codes := make([]dtc.DTC, 0, len(seed))
	for _, s := range seed {
		d, err := dtc.Parse(s.code)
		if err != nil {
			continue // unreachable: the seed list is a constant
		}
		d.Module = s.module
		d.Status = s.status
		codes = append(codes, d)
	}
	return codes
}

func (m *MockOBD) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.running = true

	go m.drive(ctx)
	return nil
}

// drive walks the live values so the dashboard visibly moves.
func (m *MockOBD) drive(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			m.rpm = clampInt(m.rpm+rand.Intn(201)-100, 600, 4000)
			m.coolant = clampFloat(m.coolant+float64(rand.Intn(21)-10)*0.1, 60, 110)
			m.oil = clampFloat(m.oil+float64(rand.Intn(21)-10)*0.1, 70, 130)
			m.kilometre += rand.Intn(2)
			m.mu.Unlock()
		}
	}
}

// Stop is safe to call more than once, and leaves the mock restartable.
func (m *MockOBD) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}
	m.cancel()
	m.cancel = nil
	m.running = false
}

func (m *MockOBD) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

func (m *MockOBD) Description() string { return "mock vehicle" }

// GetVIN returns a well-formed VIN, check digit included, so the picker and
// the decoding behind it can be exercised without a car.
//
// It decodes to a 2008 Ford, chosen because the seeded fault list includes a
// code that Ford and GM define incompatibly. Selecting the vehicle visibly
// changes what that code means, which is the whole point of the feature.
func (m *MockOBD) GetVIN(context.Context) (string, error) {
	return "1FAHP35N58W123456", nil
}

func (m *MockOBD) GetRPM(context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rpm, nil
}

func (m *MockOBD) GetCoolantTemp(context.Context) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.coolant, nil
}

func (m *MockOBD) GetOilTemp(context.Context) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.oil, nil
}

func (m *MockOBD) GetTotalKilometers(context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.kilometre, nil
}

func (m *MockOBD) GetDTCs(context.Context) ([]dtc.DTC, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]dtc.DTC, len(m.codes))
	copy(out, m.codes)
	return out, nil
}

// ClearDTCs drops stored and pending codes. Permanent ones stay, as they do on
// a real vehicle.
func (m *MockOBD) ClearDTCs(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	kept := m.codes[:0]
	for _, c := range m.codes {
		if c.Status == dtc.Permanent {
			kept = append(kept, c)
		}
	}
	m.codes = kept
	return nil
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

func clampFloat(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}

var _ obd.OBDProvider = (*MockOBD)(nil)
