// Package cmd wires the command line. It is deliberately thin: each file
// declares one command and its flags, and the work lives in internal/app.
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/fallais/cargo/pkg/log"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// version is stamped at build time with -ldflags. The default is what a
// "go build" or "go run" from a working tree reports.
var version = "dev"

// SetVersion lets the build inject a release version.
func SetVersion(v string) {
	if v != "" {
		version = v
		rootCmd.Version = v
	}
}

var rootCmd = &cobra.Command{
	Use:     "cargo",
	Version: version,
	Short:   "Read live data and trouble codes from a vehicle over OBD-II",
	Long: "cargo talks to a vehicle through an ELM327 adapter and shows live\n" +
		"readings and diagnostic trouble codes, in a terminal UI or as a\n" +
		"one-shot report.",
	// With no subcommand, scan is what people want. Both the binding and
	// the run have to be delegated: cobra does not inherit a subcommand's
	// PreRunE, and without it the scan flags never reach viper.
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd.Flags(), scanFlagNames...)
	},
	RunE: runScan,
	// Errors are logged where they happen and returned to Execute; without
	// these, cobra prints the full usage text after every runtime failure,
	// which buries the actual message.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Only genuinely global settings belong on the root command. Per-command
	// flags live with their command.
	flags := rootCmd.PersistentFlags()
	flags.Bool("debug", false, "Enable verbose logging")
	flags.String("config", "", "Config file (default: $XDG_CONFIG_HOME/cargo/cargo.yaml)")

	// Persistent flags are global, so binding them once at startup is safe.
	// Per-command flags are bound in each command's PreRunE instead: viper
	// keys share one namespace, and two commands declaring the same flag
	// name would otherwise overwrite each other's binding.
	if err := bindFlags(flags, "debug", "config"); err != nil {
		panic(err)
	}
}

func initConfig() {
	log.InitLogger(viper.GetBool("debug"))

	if path := viper.GetString("config"); path != "" {
		viper.SetConfigFile(path)
	} else {
		if dir, err := os.UserConfigDir(); err == nil {
			viper.AddConfigPath(dir + "/cargo")
		}
		viper.AddConfigPath(".")
		viper.SetConfigName("cargo")
	}

	// CARGO_PORT, CARGO_BAUD, CARGO_NO_TUI and so on.
	viper.SetEnvPrefix("CARGO")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !isNotFound(err, &notFound) {
			log.Warn("Ignoring unreadable config file")
		}
	}
}

// Execute runs the command line. It flushes the logger and exits non-zero on
// failure, so callers do not have to.
func Execute() {
	err := rootCmd.Execute()
	log.Sync()

	if err != nil {
		fmt.Fprintln(os.Stderr, "cargo:", err)
		os.Exit(1)
	}
}
