package obd

import (
	"cargo/internal/models"
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
	GetErrors() ([]models.DTCEntry, error)
}
