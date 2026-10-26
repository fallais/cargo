package cmd

import (
	"os"
	"time"

	"github.com/fallais/cargo/internal/app"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var clearFlagNames = []string{"port", "baud", "timeout", "make", "module", "yes"}

var clearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Erase trouble codes, after showing which and asking",
	Long: "Scans the vehicle, lists the codes that would be erased, and asks\n" +
		"before sending anything. Each module is cleared over the protocol it\n" +
		"was read with. Run it with the ignition on and the engine off.\n\n" +
		"Clearing does not repair anything: a fault that is still present is\n" +
		"reported again as soon as its module re-tests it, which the rescan\n" +
		"at the end shows.",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd.Flags(), clearFlagNames...)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return app.Clear(cmd.Context(), app.ClearOptions{
			Port:    viper.GetString("port"),
			Baud:    viper.GetInt("baud"),
			Timeout: viper.GetDuration("timeout"),
			Make:    viper.GetString("make"),
			Modules: viper.GetStringSlice("module"),
			Yes:     viper.GetBool("yes"),
		}, os.Stdin)
	},
	SilenceUsage: true,
}

func init() {
	flags := clearCmd.Flags()
	flags.String("port", "", "Serial port of the adapter (default: autodetect)")
	flags.Int("baud", 0, "Serial baud rate (default: try 38400, 9600, 115200, 230400, 500000)")
	flags.Duration("timeout", 5*time.Second, "Timeout for a single adapter command")
	flags.String("make", "", "Vehicle make, to describe manufacturer-specific codes")
	flags.StringSlice("module", nil, "Clear only these modules, by name as the scan reports them (repeatable)")
	flags.Bool("yes", false, "Do not ask for confirmation")

	rootCmd.AddCommand(clearCmd)
}
