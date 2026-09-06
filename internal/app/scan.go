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

	"cargo/internal/displayer"
	"cargo/internal/dtc"
	"cargo/internal/obd"
	"cargo/internal/obd/mock"
	"cargo/internal/obd/serial"
	"cargo/pkg/log"

	"go.uber.org/zap"
)

// ScanOptions is everything the scan needs. Passing a struct rather than
// reading viper in here keeps the logic testable and makes the inputs visible.
type ScanOptions struct {
	Mock    bool
	NoTUI   bool
	Port    string
	Baud    int
	Timeout time.Duration
	Make    string
}

// Scan connects to a vehicle and either runs the UI or prints one report.
func Scan(ctx context.Context, opts ScanOptions) error {
	// Ctrl-C has to reach the provider, not just the process: an adapter
	// left mid-command holds the port until it times out.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	resolver, err := dtc.NewResolver(UserCatalogPath())
	if err != nil {
		return fmt.Errorf("load trouble-code catalog: %w", err)
	}
	if opts.Make != "" {
		resolver = resolver.WithMake(opts.Make)
	}

	provider := newProvider(opts)

	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	startErr := provider.Start(startCtx)
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
	return displayer.New(provider, resolver).Run()
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
			def := resolver.Describe(c)
			fmt.Printf("  %-6s  %-10s  %s\n", c.Code, c.Status, def.Description)
		}
		fmt.Println()
	}

	fmt.Printf("%d code(s) across %d module(s).\n", len(codes), len(modules))
	return nil
}
