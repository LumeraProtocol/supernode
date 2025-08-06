package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Constants
const (
	// ManagerHomeDir is the constant home directory for sn-manager
	ManagerHomeDir = ".sn-manager"
	// GitHubRepo is the constant GitHub repository for supernode
	GitHubRepo = "LumeraProtocol/supernode"
)

// Config represents the sn-manager configuration
type Config struct {
	Updates UpdateConfig  `yaml:"updates"`
	Manager ManagerConfig `yaml:"manager"`
}

// UpdateConfig contains update-related settings
type UpdateConfig struct {
	CheckInterval  int    `yaml:"check_interval"`  // seconds between update checks
	AutoDownload   bool   `yaml:"auto_download"`   // auto-download new versions
	AutoUpgrade    bool   `yaml:"auto_upgrade"`    // auto-upgrade when available
	CurrentVersion string `yaml:"current_version"` // current active version
	KeepVersions   int    `yaml:"keep_versions"`   // number of old versions to keep
}

// ManagerConfig contains manager-specific settings
type ManagerConfig struct {
	LogLevel           string `yaml:"log_level"`            // debug, info, warn, error
	MaxRestartAttempts int    `yaml:"max_restart_attempts"` // max restarts on crash
	RestartDelay       int    `yaml:"restart_delay"`        // seconds between restarts
	ShutdownTimeout    int    `yaml:"shutdown_timeout"`     // seconds to wait for shutdown
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Updates: UpdateConfig{
			CheckInterval:  3600, // 1 hour
			AutoDownload:   true,
			AutoUpgrade:    true,
			CurrentVersion: "unknown",
			KeepVersions:   3,
		},
		Manager: ManagerConfig{
			LogLevel:           "info",
			MaxRestartAttempts: 5,
			RestartDelay:       5,
			ShutdownTimeout:    30,
		},
	}
}

// GetManagerHome returns the full path to the manager home directory
func GetManagerHome() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ManagerHomeDir)
}

// Load reads configuration from a file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Apply defaults for missing values
	if cfg.Updates.CheckInterval == 0 {
		cfg.Updates.CheckInterval = 3600
	}
	if cfg.Updates.KeepVersions == 0 {
		cfg.Updates.KeepVersions = 3
	}
	if cfg.Manager.LogLevel == "" {
		cfg.Manager.LogLevel = "info"
	}
	if cfg.Manager.MaxRestartAttempts == 0 {
		cfg.Manager.MaxRestartAttempts = 5
	}
	if cfg.Manager.RestartDelay == 0 {
		cfg.Manager.RestartDelay = 5
	}
	if cfg.Manager.ShutdownTimeout == 0 {
		cfg.Manager.ShutdownTimeout = 30
	}

	return &cfg, nil
}

// Save writes configuration to a file
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Updates.CheckInterval < 60 {
		return fmt.Errorf("updates.check_interval must be at least 60 seconds")
	}

	if c.Manager.MaxRestartAttempts < 0 {
		return fmt.Errorf("manager.max_restart_attempts cannot be negative")
	}

	return nil
}
