package obd

import (
	"context"

	"github.com/fallais/cargo/internal/dtc"
)

// An Adapter is a device we could connect to.
type Adapter struct {
	Port string
	// Detail is whatever the OS can tell us about the device, empty when
	// it cannot tell us anything.
	Detail string
	// Connected marks the one currently attached.
	Connected bool
}

// OBDProvider is the seam between the UI and the adapter behind it: a real
// adapter. Every method takes a context because each is a round trip to a
// device that may not answer.
type OBDProvider interface {
	// Start begins the provider's lifecycle. It does not connect unless
	// autoconnect is on: choosing an adapter is the user's to make.
	Start(ctx context.Context) error
	Stop()
	IsConnected() bool

	// Adapters lists the devices that could be connected to.
	Adapters() []Adapter
	// Connect attaches to one. An empty port means the first candidate.
	Connect(ctx context.Context, port string) error
	// Disconnect releases the adapter without ending the session.
	Disconnect()
	// SetAutoconnect turns background reconnection on or off.
	SetAutoconnect(on bool)
	// Autoconnect reports whether it is on.
	Autoconnect() bool

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
