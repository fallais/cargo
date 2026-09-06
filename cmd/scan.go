package cmd

import (
	"time"

	"github.com/fallais/cargo/internal/app"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// scanFlagNames is shared so the root command, which delegates to scan, binds
// exactly the same set. Letting the two drift is how "--mock" silently stops
// working on one of them.
var scanFlagNames = []string{"no-tui", "mock", "port", "baud", "timeout", "make"}

// registerScanFlags declares the scan flags on a flag set.
func registerScanFlags(flags *pflag.FlagSet) {
	flags.Bool("no-tui", false, "Print a single report and exit instead of running the UI")
	flags.Bool("mock", false, "Use a simulated vehicle instead of a real adapter")
	flags.String("port", "", "Serial port of the adapter (default: autodetect)")
	// Zero means probe the rates an ELM327 actually uses, rather than
	// guessing one.
	flags.Int("baud", 0, "Serial baud rate (default: try 38400, 9600, 115200, 230400, 500000)")
	flags.Duration("timeout", 5*time.Second, "Timeout for a single adapter command")
	flags.String("make", "", "Vehicle make, to disambiguate manufacturer-specific codes")
}

// runScan is shared by "cargo scan" and by bare "cargo".
func runScan(cmd *cobra.Command, args []string) error {
	return app.Scan(cmd.Context(), app.ScanOptions{
		Debug:   viper.GetBool("debug"),
		Mock:    viper.GetBool("mock"),
		NoTUI:   viper.GetBool("no-tui"),
		Port:    viper.GetString("port"),
		Baud:    viper.GetInt("baud"),
		Timeout: viper.GetDuration("timeout"),
		Make:    viper.GetString("make"),
	})
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Connect to a vehicle and show live data and trouble codes",
	Long: "Runs the terminal UI by default. The UI starts even with no adapter\n" +
		"attached and connects on its own once one appears.",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd.Flags(), scanFlagNames...)
	},
	RunE:         runScan,
	SilenceUsage: true,
}

func init() {
	registerScanFlags(scanCmd.Flags())

	// Bare "cargo" runs a scan, so the same flags have to work without
	// typing the subcommand name. They are declared separately rather than
	// shared, so each command's flag set is its own.
	registerScanFlags(rootCmd.Flags())

	rootCmd.AddCommand(scanCmd)
}
