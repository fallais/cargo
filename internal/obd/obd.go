package obd

import (
	"context"
)

type OBDProvider interface {
	Start(ctx context.Context) error
	Stop()
	IsConnected() bool

	GetRPM() (int, error)
	GetCoolantTemp() (float64, error)
	GetTotalKilometers() (int, error)
	GetOilTemp() (float64, error)
	GetErrors() ([]DTCEntry, error)
}

// DTCEntry represents a diagnostic trouble code with description.
type DTCEntry struct {
	Code        string
	Description string
}
