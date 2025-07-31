package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SaveConfig saves the configuration to a file
func SaveConfig(config *Config, filename string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory for config file: %w", err)
	}

	// Marshal config to YAML
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("error marshaling config to YAML: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filename, data, 0600); err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	return nil
}

// CreateDefaultConfig creates a default configuration with the specified values
func CreateDefaultConfig(keyName, identity, chainID string, keyringBackend, keyringDir string) *Config {
	// Set default values
	if keyringBackend == "" {
		keyringBackend = "test"
	}
	if keyringDir == "" {
		keyringDir = "keys"
	}

	return &Config{
		SupernodeConfig: SupernodeConfig{
			KeyName:     keyName,
			Identity:    identity,
			IpAddress:   "0.0.0.0",
			Port:        4444,
			GatewayPort: 8002,
		},
		KeyringConfig: KeyringConfig{
			Backend: keyringBackend,
			Dir:     keyringDir,
		},
		P2PConfig: P2PConfig{
			ListenAddress: "0.0.0.0",
			Port:          4445,
			DataDir:       "data/p2p",
		},
		LumeraClientConfig: LumeraClientConfig{
			GRPCAddr: "localhost:9090",
			ChainID:  chainID,
		},
		RaptorQConfig: RaptorQConfig{
			FilesDir: "raptorq_files",
		},
	}
}
