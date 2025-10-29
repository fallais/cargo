package root

import (
	"context"
	"fmt"

	"cargo/internal/displayer"
	"cargo/internal/obd"
	"cargo/internal/obd/mock"
	"cargo/internal/obd/serial"
	"cargo/pkg/log"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

func Run(cmd *cobra.Command, args []string) {
	var provider obd.OBDProvider
	if viper.GetBool("mock") {
		p := mock.New()
		provider = p
	} else {
		provider = serial.New(viper.GetInt("baud"))
	}

	// Start provider
	if err := provider.Start(context.Background()); err != nil {
		log.Fatal("failed to start OBD provider", zap.Error(err))
	}

	if viper.GetBool("no-tui") {
		printSummary(provider)
	} else {
		d := displayer.New(provider)

		err := d.Run()
		if err != nil {
			fmt.Printf("error: %v\n", err)
		}
	}
}

func printSummary(provider obd.OBDProvider) {
	errorCodes, err := provider.GetErrors()
	if err != nil {
		log.Error("failed to get error codes", zap.Error(err))
		return
	}

	fmt.Println("Current DTC Error Codes:")
	if len(errorCodes) == 0 {
		fmt.Println("No error codes.")
	} else {
		for _, code := range errorCodes {
			fmt.Printf("- %s: %s\n", code.Code, code.Description)
		}
	}
}
