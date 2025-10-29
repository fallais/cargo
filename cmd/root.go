package cmd

import (
	"fmt"
	"os"

	"cargo/internal/cmd/root"
	"cargo/pkg/log"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use: "cargo",
	Run: root.Run,
}

func init() {
	cobra.OnInitialize(initLogger)

	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")
	rootCmd.PersistentFlags().Bool("no-tui", false, "Run without TUI (for testing)")
	rootCmd.PersistentFlags().Bool("mock", false, "Use mock OBD provider")
	rootCmd.PersistentFlags().Int("baud", 84000, "Baud rate for serial connection")

	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
	viper.BindPFlag("no-tui", rootCmd.PersistentFlags().Lookup("no-tui"))
	viper.BindPFlag("mock", rootCmd.PersistentFlags().Lookup("mock"))
	viper.BindPFlag("baud", rootCmd.PersistentFlags().Lookup("baud"))

	// Set default values
	viper.SetDefault("debug", false)
	viper.SetDefault("no-tui", false)
	viper.SetDefault("mock", false)
	viper.SetDefault("baud", 84000)
}

func initLogger() {
	log.InitLogger(viper.GetBool("debug"))
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
