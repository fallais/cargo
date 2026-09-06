package cmd

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Bare "cargo" delegates to scan, and cobra does not inherit a subcommand's
// PreRunE. Both commands must therefore declare and bind the same flags, or
// "--port" works on one and is silently ignored on the other.
func TestRootAndScanShareScanFlags(t *testing.T) {
	for _, name := range scanFlagNames {
		if rootCmd.Flags().Lookup(name) == nil {
			t.Errorf("root command is missing --%s", name)
		}
		if scanCmd.Flags().Lookup(name) == nil {
			t.Errorf("scan command is missing --%s", name)
		}
	}

	if rootCmd.PreRunE == nil {
		t.Error("root command has no PreRunE, so its flags never reach viper")
	}
	if scanCmd.PreRunE == nil {
		t.Error("scan command has no PreRunE, so its flags never reach viper")
	}
}

// Every flag a command binds must exist, or bindFlags fails at run time on a
// path that may not be covered elsewhere.
func TestBindFlagsRejectsUnknown(t *testing.T) {
	if err := bindFlags(pflag.NewFlagSet("t", pflag.ContinueOnError), "nope"); err == nil {
		t.Error("bindFlags accepted a flag that does not exist")
	}
}

// A bound flag must actually be readable through viper, which is how the
// commands pass their options to internal/app.
func TestBindFlagsReachesViper(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	flags := pflag.NewFlagSet("t", pflag.ContinueOnError)
	flags.String("port", "", "")
	if err := flags.Set("port", "/dev/ttyUSB7"); err != nil {
		t.Fatal(err)
	}
	if err := bindFlags(flags, "port"); err != nil {
		t.Fatal(err)
	}

	if got := viper.GetString("port"); got != "/dev/ttyUSB7" {
		t.Errorf("viper port = %q, want /dev/ttyUSB7", got)
	}
}
