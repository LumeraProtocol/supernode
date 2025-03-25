package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"gopkg.in/yaml.v3"
)

type SupernodeConfig struct {
	KeyName   string `yaml:"key_name"`
	IpAddress string `yaml:"ip_address"`
	Port      uint16 `yaml:"port"`
	DataDir   string `yaml:"data_dir"`
}

type KeyringConfig struct {
	Backend  string `yaml:"backend"`
	Dir      string `yaml:"dir"`
	Password string `yaml:"password"`
}

type P2PConfig struct {
	ListenAddress  string `yaml:"listen_address"`
	Port           uint16 `yaml:"port"`
	DataDir        string `yaml:"data_dir"`
	BootstrapNodes string `yaml:"bootstrap_nodes"`
	ExternalIP     string `yaml:"external_ip"`
}

type LumeraClientConfig struct {
	GRPCAddr string `yaml:"grpc_addr"`
	ChainID  string `yaml:"chain_id"`
	Timeout  int    `yaml:"timeout"`
}

type Config struct {
	SupernodeConfig    `yaml:"supernode"`
	KeyringConfig      `yaml:"keyring"`
	P2PConfig          `yaml:"p2p"`
	LumeraClientConfig `yaml:"lumera"`
}

// LoadConfig loads the configuration from a file
func LoadConfig(filename string) (*Config, error) {
	ctx := context.Background()

	// Check if config file exists
	absPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("error getting absolute path for config file: %w", err)
	}

	logtrace.Info(ctx, "Loading configuration", logtrace.Fields{
		"path": absPath,
	})

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file %s does not exist", absPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	// Expand home directory in all paths
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Process SupernodeConfig
	if config.SupernodeConfig.DataDir != "" {
		config.SupernodeConfig.DataDir = expandPath(config.SupernodeConfig.DataDir, homeDir)
		if err := os.MkdirAll(config.SupernodeConfig.DataDir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create Supernode data directory: %w", err)
		}
	}

	// Process KeyringConfig
	if config.KeyringConfig.Dir != "" {
		config.KeyringConfig.Dir = expandPath(config.KeyringConfig.Dir, homeDir)
		if err := os.MkdirAll(config.KeyringConfig.Dir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create keyring directory: %w", err)
		}
	}

	// Process P2PConfig
	if config.P2PConfig.DataDir != "" {
		config.P2PConfig.DataDir = expandPath(config.P2PConfig.DataDir, homeDir)
		if err := os.MkdirAll(config.P2PConfig.DataDir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create P2P data directory: %w", err)
		}
	}

	logtrace.Info(ctx, "Configuration loaded successfully", logtrace.Fields{})
	return &config, nil
}

// expandPath handles path expansion including home directory (~)
func expandPath(path string, homeDir string) string {
	// Handle home directory expansion
	if len(path) > 0 && path[0] == '~' {
		path = filepath.Join(homeDir, path[1:])
	}

	// If path is not absolute, make it absolute based on home directory
	if !filepath.IsAbs(path) {
		path = filepath.Join(homeDir, path)
	}

	return path
}
