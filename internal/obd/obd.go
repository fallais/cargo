package obd

import (
	"context"

	"cargo/internal/dtc"
)

// OBDProvider is the seam between the UI and whatever is answering: a real
// adapter, or the mock.
//
// Every method takes a context because each one is a round trip to a device
// that may simply not answer, and a stalled read must not outlive the screen
// that asked for it.
type OBDProvider interface {
	Start(ctx context.Context) error
	Stop()
	IsConnected() bool

	// Description identifies what we are talking to, for the status line.
	Description() string

	// GetVIN reads the vehicle identification number (mode 09 PID 02).
	// It is what lets the tool identify the car itself rather than asking,
	// which matters because the make decides how manufacturer codes read.
	GetVIN(ctx context.Context) (string, error)

	GetRPM(ctx context.Context) (int, error)
	GetCoolantTemp(ctx context.Context) (float64, error)
	GetOilTemp(ctx context.Context) (float64, error)
	GetTotalKilometers(ctx context.Context) (int, error)

	// GetDTCs returns trouble codes from every module that answers, each
	// tagged with the module it came from and whether it is stored, pending
	// or permanent. Those two facts change what the code means, so they
	// travel with it rather than being flattened away.
	GetDTCs(ctx context.Context) ([]dtc.DTC, error)

	// ClearDTCs erases stored codes and extinguishes the MIL. Permanent
	// codes survive it by design: only the vehicle clears those.
	ClearDTCs(ctx context.Context) error
}
