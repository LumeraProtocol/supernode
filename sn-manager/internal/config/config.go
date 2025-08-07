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
	Updates UpdateConfig `yaml:"updates"`
}

// UpdateConfig contains update-related settings
type UpdateConfig struct {
	CheckInterval  int    `yaml:"check_interval"`  // seconds between update checks
	AutoUpgrade    bool   `yaml:"auto_upgrade"`    // auto-upgrade when available
	CurrentVersion string `yaml:"current_version"` // current active version
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Updates: UpdateConfig{
			CheckInterval:  3600, // 1 hour
			AutoUpgrade:    true, // enabled by default for security
			CurrentVersion: "",   // will be set when first binary is installed
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

	return &cfg, nil
}

// Save writes configuration to a file atomically
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to temp file then rename atomically
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Updates.CheckInterval < 60 {
		return fmt.Errorf("updates.check_interval must be at least 60 seconds")
	}

	return nil
}
