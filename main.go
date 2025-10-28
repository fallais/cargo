package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"cargo/internal/displayer"
	"cargo/internal/obd"
)

func main() {
	// CLI flags
	useMock := flag.Bool("mock", false, "use mock OBD provider")
	noTUI := flag.Bool("no-tui", false, "run without TUI (for testing)")
	flag.Parse()

	// Choose provider based on flag
	var provider obd.OBDProvider
	if *useMock {
		p := obd.NewMockOBD()
		provider = p
	} else {
		provider = obd.NewSerialOBD()
	}

	// Start provider
	if err := provider.Start(context.Background()); err != nil {
		fmt.Printf("Failed to start OBD provider: %v\n", err)
	}

	// Create displayer
	d := displayer.New(provider)
	if *noTUI {
		time.Sleep(10 * time.Second)
		// Print error codes directly (no TUI)
		if err := d.PrintErrorCodes(); err != nil {
			fmt.Printf("error: %v\n", err)
		}
	} else {
		// Run TUI
		if err := d.Run(); err != nil {
			fmt.Printf("error: %v\n", err)
		}
	}
}
