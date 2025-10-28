package main

import (
	"context"
	"flag"
	"fmt"

	"cargo/internal/displayer"
	"cargo/internal/obd"
)

func main() {
	// CLI flags
	useMock := flag.Bool("mock", false, "use mock OBD provider")
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

	// Create and run displayer
	d := displayer.New(provider)
	if err := d.Run(); err != nil {
		fmt.Printf("error: %v\n", err)
	}
}
