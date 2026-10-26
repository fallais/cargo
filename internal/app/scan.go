// Package app holds what the commands actually do, so the cobra layer in
// cmd/ stays a thin wiring shim.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/fallais/cargo/internal/displayer"
	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"github.com/fallais/cargo/internal/obd/serial"
	"github.com/fallais/cargo/internal/vehicle"
)

// ScanOptions is everything the scan needs. Passing a struct rather than
// reading viper in here keeps the logic testable and makes the inputs visible.
type ScanOptions struct {
	Debug       bool
	Autoconnect bool
	Theme       string
	NoTUI       bool
	// VIN prints the vehicle identification number and exits. Like NoTUI
	// it never takes the screen, so stdout stays a plain value for scripts.
	VIN bool
	// Sweep looks for modules at every diagnostic address before reading,
	// rather than at the usual few. It takes a couple of minutes and needs
	// the engine off.
	Sweep bool
	// Detail reads each fault's history: how often, and at what mileage.
	Detail  bool
	Port    string
	Baud    int
	Timeout time.Duration
	Make    string
}

// Scan connects to a vehicle and either runs the UI or prints one report.
func Scan(ctx context.Context, opts ScanOptions) error {
	oneShot := opts.NoTUI || opts.VIN || opts.Sweep || opts.Detail

	// Move logging off the terminal before anything logs at all. The UI is
	// about to take the screen, and a line written over it corrupts the
	// display until the next full repaint. This has to come first: setting
	// up the provider logs, and that line alone was enough to show.
	if !oneShot {
		path, err := LogToFile(opts.Debug, LogPath())
		if err != nil {
			// Keep going without a log rather than refusing to start, but
			// say so while stderr is still ours.
			fmt.Fprintf(os.Stderr, "cargo: logging disabled: %v\n", err)
			DiscardLogging()
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
		slog.Warn("Could not read the saved garage", "error", err)
		garage = &vehicle.Garage{}
	}

	make := activeMake(opts.Make, garage)
	if make != "" {
		resolver = resolver.WithMake(make)
	}

	provider := serial.New(serial.Options{
		Port:        opts.Port,
		Baud:        opts.Baud,
		ReadTimeout: opts.Timeout,
	})
	// The report has no picker, so it has to attach on its own.
	provider.SetAutoconnect(opts.Autoconnect || oneShot)

	// The session context, not a startup one: the provider keeps a
	// reconnect loop alive on whatever it is given, and a context that
	// expires would silently stop it noticing an adapter plugged in later.
	startErr := provider.Start(ctx)
	defer provider.Stop()

	// The one-shot report has nothing to show without a vehicle, so a failed
	// connection is fatal there. The UI is different: not having plugged the
	// adapter in yet is how most sessions begin, and the provider keeps
	// retrying in the background, so come up and say so on the status line.
	if oneShot {
		if startErr != nil {
			return startErr
		}
		if opts.VIN {
			return printVIN(ctx, provider)
		}
		return report(ctx, provider, resolver, reportOptions{
			sweep: opts.Sweep, detail: opts.Detail, make: make,
		})
	}

	if startErr != nil {
		slog.Info("Starting without a vehicle; will connect when one appears",
			"reason", startErr)
	}
	ui := displayer.New(provider, resolver, garage, makes)
	ui.SetTheme(displayer.ThemeNamed(opts.Theme))
	return ui.Run()
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

// activeMake is the marque lookups are made against. An explicit --make wins
// over the saved vehicle for this run, without being written back: a one-off
// override should not silently rewrite what the user configured.
func activeMake(override string, garage *vehicle.Garage) string {
	if override != "" {
		return override
	}
	if active, ok := garage.Active(); ok {
		return active.Make
	}
	return ""
}

// printVIN prints the vehicle identification number alone, so it can be
// captured by a script without parsing a report around it.
func printVIN(ctx context.Context, provider obd.OBDProvider) error {
	vin, err := provider.GetVIN(ctx)
	if err != nil {
		return fmt.Errorf("read VIN: %w", err)
	}
	fmt.Println(vin)
	return nil
}

// report prints a one-shot scan, for scripting and for when the terminal is
// not interactive.
func report(ctx context.Context, provider obd.OBDProvider, resolver *dtc.Resolver, opts reportOptions) error {
	fmt.Printf("Adapter: %s\n\n", provider.Description())

	if opts.sweep {
		if _, err := sweep(ctx, provider); err != nil {
			return err
		}
	}

	codes, err := scanCodes(ctx, provider)
	if err != nil {
		return err
	}

	var history map[int]string
	if opts.detail {
		history = readHistory(ctx, provider, codes, opts.make)
	}
	printCodes(codes, resolver, history)
	return nil
}

// reportOptions are the extras a one-shot report can be asked for.
type reportOptions struct {
	sweep  bool
	detail bool
	make   string
}

// readHistory asks each module what it kept about each of its faults, keyed
// by the code's position. Only UDS codes carry a history; one module refusing
// leaves the rest unaffected.
func readHistory(ctx context.Context, provider obd.OBDProvider, codes []dtc.DTC, make string) map[int]string {
	history := map[int]string{}
	for i, c := range codes {
		if !c.HasFailureType {
			continue
		}
		fmt.Fprintf(os.Stderr, "\rReading history %-9s (%d/%d)", c.FullCode(), i+1, len(codes))
		detail, err := provider.DTCDetail(ctx, c)
		if err != nil {
			slog.Debug("No history", "code", c.FullCode(), "error", err)
			continue
		}
		if h := detail.History(make); h.Known() {
			history[i] = h.String()
		}
	}
	fmt.Fprintln(os.Stderr)
	return history
}

// scanCodes reads every module, showing progress.
func scanCodes(ctx context.Context, provider obd.OBDProvider) ([]dtc.DTC, error) {
	// Progress goes to stderr: a walk across every module takes tens of
	// seconds, and stdout is the report itself, which stays scriptable.
	codes, err := provider.GetDTCs(ctx, func(p obd.ScanProgress) {
		fmt.Fprintf(os.Stderr, "\rScanning %-20s (%d/%d)", p.Module, p.Index, p.Total)
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("read trouble codes: %w", err)
	}
	return codes, nil
}

// printCodes lists codes grouped by the module that reported them, each with
// its history underneath where one was read.
func printCodes(codes []dtc.DTC, resolver *dtc.Resolver, history map[int]string) {
	if len(codes) == 0 {
		fmt.Println("No trouble codes reported.")
		return
	}

	// Group by module so each ECU's faults read together.
	byModule := map[string][]int{}
	for i, c := range codes {
		module := c.Module
		if module == "" {
			module = "unattributed"
		}
		byModule[module] = append(byModule[module], i)
	}

	modules := make([]string, 0, len(byModule))
	for m := range byModule {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	var failing, lamps int
	for _, module := range modules {
		fmt.Printf("%s\n", module)
		for _, i := range byModule[module] {
			c := codes[i]
			description := resolver.Describe(c).Description
			if failure := c.FailureTypeName(); failure != "" {
				description = fmt.Sprintf("%s (%s)", description, failure)
			}
			if c.FailingNow() {
				failing++
			}
			if c.WarningActive() {
				lamps++
			}
			fmt.Printf("  %-9s  %-9s  %-5s  %s\n", c.FullCode(), c.Status, flags(c), description)
			if h, ok := history[i]; ok {
				fmt.Printf("  %-9s  %-9s  %-5s  %s\n", "", "", "", h)
			}
		}
		fmt.Println()
	}

	fmt.Printf("%d code(s) across %d module(s)", len(codes), len(modules))
	if failing > 0 || lamps > 0 {
		fmt.Printf(": %d failing now, %d asking for a warning lamp", failing, lamps)
	}
	fmt.Println(".")
	if failing > 0 || lamps > 0 {
		fmt.Println("now = present when the module was asked; ! = the module is asking for a warning lamp.")
	}
}

// flags marks what a UDS status byte says beyond stored or pending: whether
// the fault is happening right now, and whether its module wants a lamp lit.
// Those are the codes behind whatever is showing on the dashboard, and without
// them a live fault reads exactly like one from last year.
func flags(c dtc.DTC) string {
	out := ""
	if c.FailingNow() {
		out += "now"
	}
	if c.WarningActive() {
		if out != "" {
			out += " "
		}
		out += "!"
	}
	return out
}
