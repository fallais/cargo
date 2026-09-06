// Package app holds what the commands actually do, so the cobra layer in
// cmd/ stays a thin wiring shim.
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/fallais/cargo/internal/displayer"
	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"github.com/fallais/cargo/internal/obd/mock"
	"github.com/fallais/cargo/internal/obd/serial"
	"github.com/fallais/cargo/internal/vehicle"
	"github.com/fallais/cargo/pkg/log"

	"go.uber.org/zap"
)

// ScanOptions is everything the scan needs. Passing a struct rather than
// reading viper in here keeps the logic testable and makes the inputs visible.
type ScanOptions struct {
	Debug   bool
	Mock    bool
	NoTUI   bool
	Port    string
	Baud    int
	Timeout time.Duration
	Make    string
}

// Scan connects to a vehicle and either runs the UI or prints one report.
func Scan(ctx context.Context, opts ScanOptions) error {
	// Move logging off the terminal before anything logs at all. The UI is
	// about to take the screen, and a line written over it corrupts the
	// display until the next full repaint. This has to come first: setting
	// up the provider logs, and that line alone was enough to show.
	if !opts.NoTUI {
		path, err := log.InitFileLogger(opts.Debug, LogPath())
		if err != nil {
			// Keep going without a log rather than refusing to start, but
			// say so while stderr is still ours.
			fmt.Fprintf(os.Stderr, "cargo: logging disabled: %v\n", err)
			log.Discard()
		} else {
			fmt.Fprintf(os.Stderr, "cargo: logging to %s\n", path)
		}
	}

	// Ctrl-C has to reach the provider, not just the process: an adapter
	// left mid-command holds the port until it times out.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	resolver, err := dtc.NewResolver(UserCatalogPath())
	if err != nil {
		return fmt.Errorf("load trouble-code catalog: %w", err)
	}

	makes, err := dtc.Makes()
	if err != nil {
		return fmt.Errorf("list catalog makes: %w", err)
	}

	garage, err := vehicle.Load(vehicle.GaragePath())
	if err != nil {
		// A corrupt garage should not stop the tool working; the user can
		// pick the vehicle again.
		log.Warn("Could not read the saved garage", zap.Error(err))
		garage = &vehicle.Garage{}
	}

	// An explicit --make wins over the saved vehicle for this run, without
	// being written back: a one-off override should not silently rewrite
	// what the user configured.
	make := opts.Make
	if make == "" {
		if active, ok := garage.Active(); ok {
			make = active.Make
		}
	}
	if make != "" {
		resolver = resolver.WithMake(make)
	}

	provider := newProvider(opts)

	// The session context, not a startup one: the provider keeps a
	// reconnect loop alive on whatever it is given, and a context that
	// expires would silently stop it noticing an adapter plugged in later.
	startErr := provider.Start(ctx)
	defer provider.Stop()

	// The one-shot report has nothing to show without a vehicle, so a failed
	// connection is fatal there. The UI is different: not having plugged the
	// adapter in yet is how most sessions begin, and the provider keeps
	// retrying in the background, so come up and say so on the status line.
	if opts.NoTUI {
		if startErr != nil {
			return startErr
		}
		return report(ctx, provider, resolver)
	}

	if startErr != nil {
		log.Info("Starting without a vehicle; will connect when one appears",
			zap.NamedError("reason", startErr))
	}
	return displayer.New(provider, resolver, garage, makes).Run()
}

func newProvider(opts ScanOptions) obd.OBDProvider {
	if opts.Mock {
		log.Info("Using the simulated vehicle")
		return mock.New()
	}

	return serial.New(serial.Options{
		Port:        opts.Port,
		Baud:        opts.Baud,
		ReadTimeout: opts.Timeout,
	})
}

// LogPath is where the UI writes its log, since it cannot use the terminal.
func LogPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "cargo.log")
	}
	return filepath.Join(dir, "cargo", "cargo.log")
}

// UserCatalogPath is where an owner can drop definitions for their own
// vehicle's manufacturer codes, which no shared catalog can carry.
func UserCatalogPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "cargo", "dtc.csv")
}

// report prints a one-shot scan, for scripting and for when the terminal is
// not interactive.
func report(ctx context.Context, provider obd.OBDProvider, resolver *dtc.Resolver) error {
	fmt.Printf("Adapter: %s\n\n", provider.Description())

	codes, err := provider.GetDTCs(ctx)
	if err != nil {
		return fmt.Errorf("read trouble codes: %w", err)
	}

	if len(codes) == 0 {
		fmt.Println("No trouble codes reported.")
		return nil
	}

	// Group by module so each ECU's faults read together.
	byModule := map[string][]dtc.DTC{}
	for _, c := range codes {
		module := c.Module
		if module == "" {
			module = "unattributed"
		}
		byModule[module] = append(byModule[module], c)
	}

	modules := make([]string, 0, len(byModule))
	for m := range byModule {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	for _, module := range modules {
		fmt.Printf("%s\n", module)
		for _, c := range byModule[module] {
			description := resolver.Describe(c).Description
			if failure := c.FailureTypeName(); failure != "" {
				description = fmt.Sprintf("%s (%s)", description, failure)
			}
			fmt.Printf("  %-9s  %-10s  %s\n", c.FullCode(), c.Status, description)
		}
		fmt.Println()
	}

	fmt.Printf("%d code(s) across %d module(s).\n", len(codes), len(modules))
	return nil
}
