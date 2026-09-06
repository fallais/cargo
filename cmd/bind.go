package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// bindFlags ties flags to viper so a value can come from a flag, the
// environment, or the config file.
func bindFlags(flags *pflag.FlagSet, names ...string) error {
	for _, name := range names {
		flag := flags.Lookup(name)
		if flag == nil {
			return fmt.Errorf("no such flag: %s", name)
		}
		if err := viper.BindPFlag(name, flag); err != nil {
			return fmt.Errorf("bind flag %s: %w", name, err)
		}
	}
	return nil
}

// isNotFound reports whether a config error is simply "no config file", which
// is the normal case and not worth warning about.
func isNotFound(err error, target *viper.ConfigFileNotFoundError) bool {
	if errors.As(err, target) {
		return true
	}
	return errors.Is(err, os.ErrNotExist)
}
