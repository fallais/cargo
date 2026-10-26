package cmd

import (
	"time"

	"github.com/fallais/cargo/internal/app"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var modulesFlagNames = []string{"port", "baud", "timeout"}

var modulesCmd = &cobra.Command{
	Use:   "modules",
	Short: "Find every module in the car and say what each one is",
	Long: "Asks every diagnostic address (700-7FF) whether a module is there,\n" +
		"reads the fault memory of each one found, and asks each what it is.\n\n" +
		"OBD-II cannot be asked which modules a car has: only the emissions\n" +
		"modules have to answer. Trying every address is how to find the rest\n" +
		"without a dealer database. It takes a couple of minutes, and needs\n" +
		"the ignition on and the engine off.",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd.Flags(), modulesFlagNames...)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return app.Modules(cmd.Context(), app.ModulesOptions{
			Port:    viper.GetString("port"),
			Baud:    viper.GetInt("baud"),
			Timeout: viper.GetDuration("timeout"),
		})
	},
	SilenceUsage: true,
}

func init() {
	flags := modulesCmd.Flags()
	flags.String("port", "", "Serial port of the adapter (default: autodetect)")
	flags.Int("baud", 0, "Serial baud rate (default: try 38400, 9600, 115200, 230400, 500000)")
	flags.Duration("timeout", 5*time.Second, "Timeout for a single adapter command")

	rootCmd.AddCommand(modulesCmd)
}
