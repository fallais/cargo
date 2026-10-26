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

// ScanProgress reports how far a module scan has got.
type ScanProgress struct {
	// Module is the ECU about to be probed.
	Module string
	// Index is its place in the walk, counting from one, and Total is how
	// many addresses this pass will try.
	Index, Total int
	// Answered counts the modules that have replied so far. A scan that
	// reaches four addresses out of nine has found nothing about the other
	// five, which is a different thing from finding them healthy.
	Answered int
	// Done marks the final report of a walk, sent once the last module has
	// been probed rather than before it.
	Done bool
}

// DTCDetail is what a module holds about one code beyond its status.
//
// Both records are manufacturer-defined past their headers. ISO 14229-1 fixes
// how to ask and how the reply is framed, but the meaning of the bytes inside
// is the ECU maker's, so what cannot be decoded honestly is carried through
// raw rather than guessed at.
type DTCDetail struct {
	// Records are the extended data records, each as the module sent it.
	// Their numbering and contents are the manufacturer's; History reads
	// the ones whose layout is known.
	Records []ExtendedRecord
	// Snapshot is the freeze-frame payload verbatim: what the module
	// recorded about the vehicle at the moment the fault was stored.
	Snapshot []byte
	// SnapshotIdentifiers is how many data identifiers the snapshot holds.
	// Their meanings are per-ECU, so the count is reported without
	// pretending to name them.
	SnapshotIdentifiers int
}

// ExtendedRecord is one DTC extended data record (UDS 0x19 0x06).
type ExtendedRecord struct {
	Number byte
	Data   []byte
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

	// GetVoltage reads the supply at the diagnostic connector, which is
	// battery voltage. It comes from the adapter rather than the vehicle,
	// so it answers even when no ECU will.
	GetVoltage(ctx context.Context) (float64, error)

	GetRPM(ctx context.Context) (int, error)
	GetCoolantTemp(ctx context.Context) (float64, error)
	GetOilTemp(ctx context.Context) (float64, error)
	// GetDistanceSinceClear reads mode 01 PID 31. This is not the odometer:
	// it counts from the last time the trouble codes were erased.
	GetDistanceSinceClear(ctx context.Context) (int, error)

	// GetDTCs returns codes from every module that answers, each tagged
	// with its module and status. Both change what a code means, so they
	// travel with it rather than being flattened away.
	//
	// progress, when non-nil, is called before each module is probed. A
	// full walk is tens of seconds of serial round trips and a caller with
	// a screen needs to show that something is happening.
	GetDTCs(ctx context.Context, progress func(ScanProgress)) ([]dtc.DTC, error)

	// SweepModules asks every diagnostic address for a module and makes
	// what answers the list later scans read. Only with the engine off: it
	// sends requests to identifiers that may carry ordinary traffic.
	SweepModules(ctx context.Context, progress func(ScanProgress)) ([]Module, error)

	// Modules returns the modules the last scan or sweep settled on.
	Modules() []Module

	// Identify asks a module from the last scan or sweep what it is.
	Identify(ctx context.Context, module string) (Identity, error)

	// RescanModules widens the next scan back to every known address,
	// undoing the narrowing GetDTCs applies once it has seen the vehicle.
	RescanModules()

	// DTCDetail asks the module that reported a code what else it knows:
	// how often the fault has happened, and what was recorded when it did.
	// For an intermittent fault that is the difference between "this
	// happens on every drive" and "this happened once, last winter".
	DTCDetail(ctx context.Context, code dtc.DTC) (DTCDetail, error)

	// ClearDTCs erases the codes held by the named modules, as found by
	// the last GetDTCs, each over the protocol it was read with. It
	// returns one result per module, because one refusal should not hide
	// that the others were cleared. Permanent codes survive by design:
	// only the vehicle clears those.
	ClearDTCs(ctx context.Context, modules []string) []ClearResult
}

// ClearResult is the outcome of clearing one module.
type ClearResult struct {
	Module string
	Err    error
}
