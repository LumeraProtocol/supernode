package cmd

import (
	"fmt"
	"os"

	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile   string
	appConfig *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "supernode",
	Short: "Lumera CLI tool for key management",
	Long: `A command line tool for managing Lumera blockchain keys.
This application allows you to create and recover keys using mnemonics.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for help command
		if cmd.Name() == "help" {
			return nil
		}

		// If config file path is not specified, use the default in current directory
		if cfgFile == "" {
			cfgFile = "config.yml"
		}

		// Load configuration
		var err error
		appConfig, err = config.LoadConfig(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config file %s: %w", cfgFile, err)
		}

		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Allow user to override config file location with --config flag
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file path (default is ./config.yaml)")
}
