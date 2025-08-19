package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/spf13/cobra"
)

var (
	baseDir   string
	appConfig *config.Config
)

const (
	DefaultConfigFile = "config.yml"
	DefaultBaseDir    = ".supernode"
)

// fileExists checks if a file exists at the given path
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// getConfigFile returns the config file path in base directory
func getConfigFile(baseDirPath string) string {
	return filepath.Join(baseDirPath, DefaultConfigFile)
}

func setupBaseDir() (string, error) {
	if baseDir != "" {
		return baseDir, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, DefaultBaseDir), nil
}

var rootCmd = &cobra.Command{
	Use:   "supernode",
	Short: "Lumera CLI tool for key management",
	Long: `A command line tool for managing Lumera blockchain keys.
This application allows you to create and recover keys using mnemonics.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for help command, init command, and version command
		if cmd.Name() == "help" || cmd.Name() == "init" || cmd.Name() == "version" {
			// For init command, we still need to set up the base directory
			if cmd.Name() == "init" {
				var err error
				baseDir, err = setupBaseDir()
				if err != nil {
					return err
				}
			}
			return nil
		}

		// Setup base directory
		var err error
		baseDir, err = setupBaseDir()
		if err != nil {
			return err
		}

		// Get config file path
		cfgFile := getConfigFile(baseDir)
		if !fileExists(cfgFile) {
			return fmt.Errorf("no config file found in base directory (%s)", baseDir)
		}

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
	// Use default values in flag descriptions
	rootCmd.PersistentFlags().StringVarP(&baseDir, "basedir", "d", "",
		fmt.Sprintf("Base directory for all data (default is ~/%s)", DefaultBaseDir))
}
