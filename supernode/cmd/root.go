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

// fileExists checks if a file exists at the given path
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// findConfigFile searches for config files in multiple locations
func findConfigFile() string {
	// If config file is explicitly specified, use it
	if cfgFile != "" {
		return cfgFile
	}

	// Try current working directory first (for go run)
	workingDir, err := os.Getwd()
	if err == nil {
		yamlPath := filepath.Join(workingDir, "config.yaml")
		ymlPath := filepath.Join(workingDir, "config.yml")

		if fileExists(yamlPath) {
			return yamlPath
		}
		if fileExists(ymlPath) {
			return ymlPath
		}
	}

	// Then try executable directory (for binary)
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		yamlPath := filepath.Join(execDir, "config.yaml")
		ymlPath := filepath.Join(execDir, "config.yml")

		if fileExists(yamlPath) {
			return yamlPath
		}
		if fileExists(ymlPath) {
			return ymlPath
		}
	}

	return ""
}

// setupBaseDir configures the base directory if not specified
func setupBaseDir() (string, error) {
	if baseDir != "" {
		return baseDir, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".supernode"), nil
}

// logConfig logs information about config and base directory
func logConfig(configPath, baseDirPath string) {
	// For config file
	absPath, err := filepath.Abs(configPath)
	if err == nil {
		fmt.Printf("Using config file: %s\n", absPath)
	} else {
		fmt.Printf("Using config file: %s\n", configPath)
	}

	// For base directory
	fmt.Printf("Using base directory: %s\n", baseDirPath)
}

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

		// Setup base directory
		setupDir, err := setupBaseDir()
		if err != nil {
			return err
		}
		baseDir = setupDir

		// Find config file
		cfgFile = findConfigFile()
		if cfgFile == "" {
			return fmt.Errorf("no config file found in working directory or executable directory")
		}

		// Log configuration
		logConfig(cfgFile, baseDir)

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
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "Config file path (default is ./config.yaml or ./config.yml)")
	rootCmd.PersistentFlags().StringVarP(&baseDir, "basedir", "d", "", "Base directory for all data (default is ~/.supernode)")
}
