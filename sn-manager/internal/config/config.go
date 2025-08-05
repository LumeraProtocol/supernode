package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the sn-manager configuration
type Config struct {
	SuperNode SuperNodeConfig `yaml:"supernode"`
	Updates   UpdateConfig    `yaml:"updates"`
	Manager   ManagerConfig   `yaml:"manager"`
}

// SuperNodeConfig contains SuperNode-specific settings
type SuperNodeConfig struct {
	Home       string `yaml:"home"`        // Path to supernode's config directory
	Args       string `yaml:"args"`        // Additional arguments to pass to supernode
	BinaryPath string `yaml:"binary_path"` // Binary path (if not using managed versions)
}

// UpdateConfig contains update-related settings
type UpdateConfig struct {
	GitHubRepo     string `yaml:"github_repo"`     // GitHub repository (owner/repo format)
	CheckInterval  int    `yaml:"check_interval"`  // Check interval in seconds
	AutoDownload   bool   `yaml:"auto_download"`   // Auto-download new versions
	AutoUpgrade    bool   `yaml:"auto_upgrade"`    // Auto-upgrade when available
	CurrentVersion string `yaml:"current_version"` // Current active version
	KeepVersions   int    `yaml:"keep_versions"`   // Number of old versions to keep
}

// ManagerConfig contains manager-specific settings
type ManagerConfig struct {
	LogLevel           string `yaml:"log_level"`            // Log level: debug, info, warn, error
	MaxRestartAttempts int    `yaml:"max_restart_attempts"` // Max restart attempts on crash
	RestartDelay       int    `yaml:"restart_delay"`        // Delay between restarts (seconds)
	ShutdownTimeout    int    `yaml:"shutdown_timeout"`     // Shutdown timeout (seconds)
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/user"
	}

	return &Config{
		SuperNode: SuperNodeConfig{
			Home: home + "/.supernode",
			Args: "",
		},
		Updates: UpdateConfig{
			GitHubRepo:     "LumeraProtocol/supernode",
			CheckInterval:  3600, // 1 hour in seconds
			AutoDownload:   true,
			AutoUpgrade:    false,
			CurrentVersion: "unknown",
			KeepVersions:   3,
		},
		Manager: ManagerConfig{
			LogLevel:           "info",
			MaxRestartAttempts: 5,
			RestartDelay:       5,  // 5 seconds
			ShutdownTimeout:    30, // 30 seconds
		},
	}
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
	if cfg.Updates.GitHubRepo == "" {
		cfg.Updates.GitHubRepo = "LumeraProtocol/supernode"
	}
	if cfg.Updates.CheckInterval == 0 {
		cfg.Updates.CheckInterval = 3600 // 1 hour
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
		cfg.Manager.RestartDelay = 5 // 5 seconds
	}
	if cfg.Manager.ShutdownTimeout == 0 {
		cfg.Manager.ShutdownTimeout = 30 // 30 seconds
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
	if c.SuperNode.Home == "" {
		return fmt.Errorf("supernode.home is required")
	}

	if c.Updates.GitHubRepo == "" {
		return fmt.Errorf("updates.github_repo is required")
	}

	if c.Updates.CheckInterval < 60 {
		return fmt.Errorf("updates.check_interval must be at least 60 seconds")
	}

	if c.Manager.MaxRestartAttempts < 0 {
		return fmt.Errorf("manager.max_restart_attempts cannot be negative")
	}

	return nil
}
