package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile   string
	baseDir   string
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

		// Set default base directory to home if not specified
		if baseDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			baseDir = filepath.Join(homeDir, ".supernode")
		}

		// If config file path not specified, use the one next to the binary
		if cfgFile == "" {
			// Get the directory where the binary is located
			execPath, err := os.Executable()
			if err == nil {
				execDir := filepath.Dir(execPath)
				cfgFile = filepath.Join(execDir, "config.yaml")

				// If not found, try config.yml
				if _, err := os.Stat(cfgFile); err != nil {
					ymlConfig := filepath.Join(execDir, "config.yml")
					if _, err := os.Stat(ymlConfig); err == nil {
						cfgFile = ymlConfig
					}
				}
			}
		}

		// Get absolute path for clarity in logs
		absPath, err := filepath.Abs(cfgFile)
		if err == nil {
			fmt.Printf("Using config file: %s\n", absPath)
		} else {
			fmt.Printf("Using config file: %s\n", cfgFile)
		}

		fmt.Printf("Using base directory: %s\n", baseDir)

		// Load configuration
		appConfig, err = config.LoadConfig(cfgFile, baseDir)
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
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "Config file path (default ./config.yaml )")
	rootCmd.PersistentFlags().StringVarP(&baseDir, "basedir", "d", "", "Base directory for all data (default is ~/.supernode)")
}
