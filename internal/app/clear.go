package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"github.com/fallais/cargo/internal/obd/serial"
	"github.com/fallais/cargo/internal/vehicle"
)

// ClearOptions is everything a clear needs.
type ClearOptions struct {
	Port    string
	Baud    int
	Timeout time.Duration
	Make    string
	// Modules limits the clear to these modules, by name. Empty means
	// every module that reported a code.
	Modules []string
	// Yes skips the confirmation, for scripts.
	Yes bool
}

// Clear erases trouble codes, after showing exactly which ones and asking.
//
// It is the one command that changes the car, so it is careful about it: the
// engine has to be off, only modules holding codes are touched, and nothing
// is sent until the user has seen the list. A rescan afterwards shows what
// came straight back, which is the useful part: a fault that is still there
// is reported again as soon as its module re-tests it.
func Clear(ctx context.Context, opts ClearOptions, in io.Reader) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	resolver, err := dtc.NewResolver(UserCatalogPath())
	if err != nil {
		return fmt.Errorf("load trouble-code catalog: %w", err)
	}
	garage, err := vehicle.Load(vehicle.GaragePath())
	if err != nil {
		slog.Warn("Could not read the saved garage", "error", err)
		garage = &vehicle.Garage{}
	}
	if make := activeMake(opts.Make, garage); make != "" {
		resolver = resolver.WithMake(make)
	}

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

	// Mode 04 also resets the engine's readiness monitors and, on many
	// controllers, what it has learned, and some refuse it while running.
	if err := requireEngineOff(ctx, provider); err != nil {
		return err
	}

	codes, err := scanCodes(ctx, provider)
	if err != nil {
		return err
	}

	selected, modules, err := selectCodes(codes, opts.Modules)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		fmt.Println("No trouble codes to clear.")
		return nil
	}

	printCodes(selected, resolver, nil)
	fmt.Println()

	if !opts.Yes {
		question := fmt.Sprintf("Clear %d code(s) from %s?", len(selected), strings.Join(modules, ", "))
		if !confirm(in, os.Stdout, question) {
			fmt.Println("Nothing cleared.")
			return nil
		}
	}

	var failed []string
	for _, r := range provider.ClearDTCs(ctx, modules) {
		if r.Err != nil {
			fmt.Printf("  %-20s  not cleared: %v\n", r.Module, r.Err)
			failed = append(failed, r.Module)
			continue
		}
		fmt.Printf("  %-20s  cleared\n", r.Module)
	}

	// Give the modules a moment to re-run the tests that run at once, so
	// a fault that is still present shows in the rescan rather than on
	// the next drive.
	fmt.Println("\nRescanning...")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	after, err := scanCodes(ctx, provider)
	if err != nil {
		return err
	}
	remaining, _, err := selectCodes(after, opts.Modules)
	if err != nil && !errors.Is(err, errNoSuchModule) {
		return err
	}
	if len(remaining) == 0 {
		fmt.Println("Nothing came back.")
	} else {
		fmt.Println("Still reported. A fault that is present now comes straight back,")
		fmt.Println("and permanent codes stay until the vehicle clears them itself:")
		fmt.Println()
		printCodes(remaining, resolver, nil)
	}

	if len(failed) > 0 {
		return fmt.Errorf("could not clear %s", strings.Join(failed, ", "))
	}
	return nil
}

// requireEngineOff refuses to go on with the engine running. The commands
// that use it either change the car or send requests to addresses that may
// carry ordinary traffic, and the procedure for both is ignition on, engine
// off. A failed read is not a running engine: with the engine stopped some
// ECUs simply do not answer for RPM.
func requireEngineOff(ctx context.Context, provider obd.OBDProvider) error {
	if rpm, err := provider.GetRPM(ctx); err == nil && rpm > 0 {
		return fmt.Errorf("the engine is running (%d rpm): switch it off, "+
			"leave the ignition on, and run this again", rpm)
	}
	return nil
}

var errNoSuchModule = errors.New("no module reported codes under that name")

// selectCodes picks the codes a clear applies to and the modules holding
// them, in a stable order. Names match case-insensitively. Asking for a module
// that reported nothing is an error naming the ones that did, because a typo
// that silently cleared nothing would look like success.
func selectCodes(codes []dtc.DTC, wanted []string) ([]dtc.DTC, []string, error) {
	present := map[string]string{} // lowercased name -> name as reported
	for _, c := range codes {
		present[strings.ToLower(c.Module)] = c.Module
	}

	keep := present
	if len(wanted) > 0 {
		keep = map[string]string{}
		for _, w := range wanted {
			name, ok := present[strings.ToLower(strings.TrimSpace(w))]
			if !ok {
				known := make([]string, 0, len(present))
				for _, n := range present {
					known = append(known, fmt.Sprintf("%q", n))
				}
				sort.Strings(known)
				hint := "none reported any"
				if len(known) > 0 {
					hint = "codes came from " + strings.Join(known, ", ")
				}
				return nil, nil, fmt.Errorf("%w: %q (%s)", errNoSuchModule, w, hint)
			}
			keep[strings.ToLower(name)] = name
		}
	}

	var selected []dtc.DTC
	for _, c := range codes {
		if _, ok := keep[strings.ToLower(c.Module)]; ok {
			selected = append(selected, c)
		}
	}

	modules := make([]string, 0, len(keep))
	for _, name := range keep {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	return selected, modules, nil
}

// confirm asks a yes/no question and defaults to no. Anything but an explicit
// yes, including no terminal to answer from, declines.
func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [y/N] ", question)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}
