package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
	"github.com/spf13/cobra"
)

var (
	// Config flags
	cfgCheckInterval      int
	cfgAutoDownload       bool
	cfgAutoUpgrade        bool
	cfgCurrentVersion     string
	cfgKeepVersions       int
	cfgLogLevel           string
	cfgMaxRestartAttempts int
	cfgRestartDelay       int
	cfgShutdownTimeout    int
	forceInit             bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize sn-manager configuration",
	Long:  `Initialize the sn-manager configuration file and directory structure.`,
	RunE:  runInit,
}

func init() {
	// Get default config for flag defaults
	def := config.DefaultConfig()

	// Force flag
	initCmd.Flags().BoolVar(&forceInit, "force", false, "Force re-initialization by removing existing directory")

	// Updates config
	initCmd.Flags().IntVar(&cfgCheckInterval, "check-interval", def.Updates.CheckInterval, "Update check interval (seconds)")
	initCmd.Flags().BoolVar(&cfgAutoDownload, "auto-download", def.Updates.AutoDownload, "Auto-download new versions")
	initCmd.Flags().BoolVar(&cfgAutoUpgrade, "auto-upgrade", def.Updates.AutoUpgrade, "Auto-upgrade when available")
	initCmd.Flags().StringVar(&cfgCurrentVersion, "current-version", def.Updates.CurrentVersion, "Current version")
	initCmd.Flags().IntVar(&cfgKeepVersions, "keep-versions", def.Updates.KeepVersions, "Number of old versions to keep")

	// Manager config
	initCmd.Flags().StringVar(&cfgLogLevel, "log-level", def.Manager.LogLevel, "Log level (debug/info/warn/error)")
	initCmd.Flags().IntVar(&cfgMaxRestartAttempts, "max-restart-attempts", def.Manager.MaxRestartAttempts, "Max restart attempts on crash")
	initCmd.Flags().IntVar(&cfgRestartDelay, "restart-delay", def.Manager.RestartDelay, "Delay between restarts (seconds)")
	initCmd.Flags().IntVar(&cfgShutdownTimeout, "shutdown-timeout", def.Manager.ShutdownTimeout, "Shutdown timeout (seconds)")
}

func runInit(cmd *cobra.Command, args []string) error {
	managerHome := config.GetManagerHome()
	configPath := filepath.Join(managerHome, "config.yml")

	// Check if already initialized
	if _, err := os.Stat(configPath); err == nil {
		if !forceInit {
			return fmt.Errorf("already initialized at %s. Use --force to re-initialize", managerHome)
		}

		// Force mode: remove existing directory
		fmt.Printf("Removing existing directory at %s...\n", managerHome)
		if err := os.RemoveAll(managerHome); err != nil {
			return fmt.Errorf("failed to remove existing directory: %w", err)
		}
	}

	// Create directory structure
	dirs := []string{
		managerHome,
		filepath.Join(managerHome, "binaries"),
		filepath.Join(managerHome, "downloads"),
		filepath.Join(managerHome, "logs"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Start with default config
	cfg := config.DefaultConfig()

	// Override with provided flags only
	if cmd.Flags().Changed("check-interval") {
		cfg.Updates.CheckInterval = cfgCheckInterval
	}
	if cmd.Flags().Changed("auto-download") {
		cfg.Updates.AutoDownload = cfgAutoDownload
	}
	if cmd.Flags().Changed("auto-upgrade") {
		cfg.Updates.AutoUpgrade = cfgAutoUpgrade
	}
	if cmd.Flags().Changed("current-version") {
		cfg.Updates.CurrentVersion = cfgCurrentVersion
	}
	if cmd.Flags().Changed("keep-versions") {
		cfg.Updates.KeepVersions = cfgKeepVersions
	}
	if cmd.Flags().Changed("log-level") {
		cfg.Manager.LogLevel = cfgLogLevel
	}
	if cmd.Flags().Changed("max-restart-attempts") {
		cfg.Manager.MaxRestartAttempts = cfgMaxRestartAttempts
	}
	if cmd.Flags().Changed("restart-delay") {
		cfg.Manager.RestartDelay = cfgRestartDelay
	}
	if cmd.Flags().Changed("shutdown-timeout") {
		cfg.Manager.ShutdownTimeout = cfgShutdownTimeout
	}

	// Save config
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}
