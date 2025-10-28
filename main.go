package main

import (
	"context"
	"fmt"

	"cargo/internal/displayer"
	"cargo/internal/obd"
)

func main() {
	provider := obd.NewSerialOBD()
	err := provider.Start(context.Background())
	if err != nil {
		fmt.Printf("Failed to start OBD provider: %v\n", err)
	}

	d := displayer.New(provider)
	if err := d.Run(); err != nil {
		fmt.Printf("error: %v\n", err)
	}
}
