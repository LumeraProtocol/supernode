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

const (
	DefaultConfigFile = "config.yaml"
	DefaultBaseDir    = ".supernode"
)

// fileExists checks if a file exists at the given path
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// findConfigFile searches for config files in base directory only
func findConfigFile(baseDirPath string) string {
	// If config file is explicitly specified, use it
	if cfgFile != "" {
		return cfgFile
	}

	// Only search in base directory as default
	if baseDirPath != "" {
		searchPaths := []string{
			filepath.Join(baseDirPath, DefaultConfigFile),
			filepath.Join(baseDirPath, "config.yml"),
		}

		// Return first existing config file
		for _, path := range searchPaths {
			if fileExists(path) {
				return path
			}
		}
	}

	return ""
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

// logConfig logs information about config and base directory
func logConfig(configPath, baseDirPath string) {
	// For config file
	if absPath, err := filepath.Abs(configPath); err == nil {
		fmt.Printf("Using config file: %s\n", absPath)
	} else {
		fmt.Printf("Using config file: %s\n", configPath)
	}

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
		var err error
		baseDir, err = setupBaseDir()
		if err != nil {
			return err
		}

		// Find config file (only searches in base directory by default)
		cfgFile = findConfigFile(baseDir)
		if cfgFile == "" {
			return fmt.Errorf("no config file found in base directory (%s)", baseDir)
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
	// Use default values in flag descriptions
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "",
		fmt.Sprintf("Config file path (default is ~/%s/%s)", DefaultBaseDir, DefaultConfigFile))
	rootCmd.PersistentFlags().StringVarP(&baseDir, "basedir", "d", "",
		fmt.Sprintf("Base directory for all data (default is ~/%s)", DefaultBaseDir))
}
