package obd

import (
	"context"

	"github.com/fallais/cargo/internal/dtc"
)

// OBDProvider is the seam between the UI and whatever is answering: a real
// adapter, or the mock. Every method takes a context because each is a round
// trip to a device that may not answer.
type OBDProvider interface {
	Start(ctx context.Context) error
	Stop()
	IsConnected() bool

	// Description identifies what we are talking to, for the status line.
	Description() string

	// GetVIN reads the vehicle identification number (mode 09 PID 02), so
	// the make can be determined without asking the user.
	GetVIN(ctx context.Context) (string, error)

	GetRPM(ctx context.Context) (int, error)
	GetCoolantTemp(ctx context.Context) (float64, error)
	GetOilTemp(ctx context.Context) (float64, error)
	GetTotalKilometers(ctx context.Context) (int, error)

	// GetDTCs returns codes from every module that answers, each tagged
	// with its module and status. Both change what a code means, so they
	// travel with it rather than being flattened away.
	GetDTCs(ctx context.Context) ([]dtc.DTC, error)

	// ClearDTCs erases stored codes and the MIL. Permanent codes survive by
	// design: only the vehicle clears those.
	ClearDTCs(ctx context.Context) error
}
