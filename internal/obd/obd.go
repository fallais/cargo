package obd

import (
	"context"
)

// OBDProvider abstracts access to OBD-II device.
// It handles finding the device, reconnecting, and getting values.
type OBDProvider interface {
	Start(ctx context.Context) error
	Stop()
	GetRPM() (int, error)
	GetCoolantTemp() (float64, error)
	GetTotalKilometers() (int, error)
	GetOilTemp() (float64, error)
	GetErrors() ([]DTCEntry, error)
	Connected() bool
}

// DTCEntry represents a diagnostic trouble code with description.
type DTCEntry struct {
	Code        string
	Description string
}
