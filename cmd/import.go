package cmd

import (
	"fmt"
	"time"

	"cargo/internal/dtcimport"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Rebuild the embedded trouble-code catalog from the open datasets",
	Long: "Fetches the open DTC datasets, classifies each code by whether\n" +
		"ISO/SAE or a manufacturer controls its definition, and writes the\n" +
		"two CSV files that internal/dtc embeds.\n\n" +
		"This is a maintenance command. Its output is committed, so review\n" +
		"the diff before merging, and rebuild afterwards for the change to\n" +
		"reach the binary.",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd.Flags(), "out", "cache", "offline", "fetch-timeout", "credits")
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := dtcimport.Run(cmd.Context(), dtcimport.Options{
			OutDir:   viper.GetString("out"),
			CacheDir: viper.GetString("cache"),
			Offline:  viper.GetBool("offline"),
			Timeout:  viper.GetDuration("fetch-timeout"),
		})
		if err != nil {
			return err
		}

		fmt.Printf("Imported %d source file(s), skipped %d.\n", result.Sources, result.Skipped)
		fmt.Printf("  generic       %6d codes\n", result.Generic)
		fmt.Printf("  manufacturer  %6d definitions across %d make(s)\n",
			result.Manufacturer, result.Makes)
		fmt.Printf("  dropped       %6d redundant, %d conflicting duplicate(s)\n",
			result.Redundant, result.Conflicts)

		fmt.Println("\nSources:")
		for _, l := range result.Licences {
			fmt.Printf("  %-8s %s (%s)\n", l.SPDX, l.URL, l.Holder)
		}

		if path := viper.GetString("credits"); path != "" {
			if err := dtcimport.WriteCredits(path, result); err != nil {
				return fmt.Errorf("write credits: %w", err)
			}
			fmt.Printf("\nAttribution written to %s\n", path)
		}
		return nil
	},
	SilenceUsage: true,
}

func init() {
	flags := importCmd.Flags()
	flags.String("out", "internal/dtc/data", "Directory to write the catalog CSVs into")
	flags.String("cache", "", "Directory for downloaded sources (default: a temp dir)")
	flags.Bool("offline", false, "Use only already-cached sources")
	flags.Duration("fetch-timeout", 30*time.Second, "Timeout for a single download")
	flags.String("credits", "CREDITS.md", "Attribution file to regenerate (empty to skip)")

	rootCmd.AddCommand(importCmd)
}
