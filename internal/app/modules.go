package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fallais/cargo/internal/obd"
	"github.com/fallais/cargo/internal/obd/serial"
)

// ModulesOptions is everything a module listing needs.
type ModulesOptions struct {
	Port    string
	Baud    int
	Timeout time.Duration
}

// Modules lists every module the car has: it asks every diagnostic address,
// reads the fault memory of each module found, and asks each what it is.
//
// It answers a question OBD-II cannot be asked directly. Only the emissions
// modules have to answer the broadcast; every other address is a
// manufacturer's convention, so the only way to know them without a dealer
// database is to try them all. That takes a couple of minutes.
func Modules(ctx context.Context, opts ModulesOptions) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	provider := serial.New(serial.Options{
		Port:        opts.Port,
		Baud:        opts.Baud,
		ReadTimeout: opts.Timeout,
	})
	provider.SetAutoconnect(true)
	if err := provider.Start(ctx); err != nil {
		return err
	}
	defer provider.Stop()

	fmt.Printf("Adapter: %s\n\n", provider.Description())

	modules, err := sweep(ctx, provider)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		fmt.Println("No module answered.")
		return nil
	}

	codes, err := scanCodes(ctx, provider)
	if err != nil {
		return err
	}
	faults := map[string]int{}
	for _, c := range codes {
		faults[c.Module]++
	}

	fmt.Printf("%-7s  %-5s  %-20s  %-12s  %-6s  %s\n", "Address", "Reply", "Module", "Protocol", "Faults", "Identity")
	for _, m := range provider.Modules() {
		identity, err := provider.Identify(ctx, m.Name)
		who := identity.String()
		if err != nil || who == "" {
			who = "-"
		}

		count := "-"
		if m.Readable {
			count = fmt.Sprint(faults[m.Name])
		}
		fmt.Printf("%-7s  %-5s  %-20s  %-12s  %-6s  %s\n",
			fmt.Sprintf("%03X", m.Request), fmt.Sprintf("%03X", m.Response),
			m.Name, protocolName(m), count, who)
	}
	fmt.Println("\nFaults \"-\": the module answers to its address but its fault memory did not.")
	return nil
}

// sweep finds every module, with the engine off, showing progress.
func sweep(ctx context.Context, provider obd.OBDProvider) ([]obd.Module, error) {
	if err := requireEngineOff(ctx, provider); err != nil {
		return nil, err
	}
	modules, err := provider.SweepModules(ctx, func(p obd.ScanProgress) {
		fmt.Fprintf(os.Stderr, "\rSweeping %-12s (%d/%d), %d found", p.Module, p.Index, p.Total, p.Answered)
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	return modules, nil
}

// protocolName says how a module's faults were read.
func protocolName(m obd.Module) string {
	if !m.Readable {
		return "-"
	}
	var name string
	switch m.Manufacturer {
	case obd.UDS:
		name = "UDS"
	case obd.KWP2000:
		name = "KWP2000"
	}
	switch {
	case m.Standard && name != "":
		return "OBD-II + " + name
	case m.Standard || name == "":
		return "OBD-II"
	}
	return name
}
