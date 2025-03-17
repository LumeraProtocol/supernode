// File: config/config.go
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
	ctx := logtrace.CtxWithCorrelationID(context.Background(), "config-loader")

	// Check if config file exists
	absPath, err := filepath.Abs(filename)
	if err != nil {
		logtrace.Error(ctx, "Failed to get absolute path for config file", logtrace.Fields{
			"filename": filename,
			"error":    err.Error(),
		})
		return nil, fmt.Errorf("error getting absolute path for config file: %w", err)
	}

	logtrace.Info(ctx, "Loading configuration", logtrace.Fields{
		"path": absPath,
	})

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		logtrace.Error(ctx, "Config file does not exist", logtrace.Fields{
			"path": absPath,
		})
		return nil, fmt.Errorf("config file %s does not exist", absPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		logtrace.Error(ctx, "Failed to read config file", logtrace.Fields{
			"path":  absPath,
			"error": err.Error(),
		})
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		logtrace.Error(ctx, "Failed to parse config file", logtrace.Fields{
			"path":  absPath,
			"error": err.Error(),
		})
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	// Expand home directory in all paths
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logtrace.Error(ctx, "Failed to get home directory", logtrace.Fields{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Process SupernodeConfig
	if config.SupernodeConfig.DataDir != "" {
		expandedDir := expandPath(config.SupernodeConfig.DataDir, homeDir)
		logtrace.Info(ctx, "Expanding supernode data directory", logtrace.Fields{
			"original": config.SupernodeConfig.DataDir,
			"expanded": expandedDir,
		})
		config.SupernodeConfig.DataDir = expandedDir

		if err := os.MkdirAll(config.SupernodeConfig.DataDir, 0700); err != nil {
			logtrace.Error(ctx, "Failed to create supernode data directory", logtrace.Fields{
				"dir":   config.SupernodeConfig.DataDir,
				"error": err.Error(),
			})
			return nil, fmt.Errorf("failed to create Supernode data directory: %w", err)
		}
	}

	// Process KeyringConfig
	if config.KeyringConfig.Dir != "" {
		expandedDir := expandPath(config.KeyringConfig.Dir, homeDir)
		logtrace.Info(ctx, "Expanding keyring directory", logtrace.Fields{
			"original": config.KeyringConfig.Dir,
			"expanded": expandedDir,
		})
		config.KeyringConfig.Dir = expandedDir

		if err := os.MkdirAll(config.KeyringConfig.Dir, 0700); err != nil {
			logtrace.Error(ctx, "Failed to create keyring directory", logtrace.Fields{
				"dir":   config.KeyringConfig.Dir,
				"error": err.Error(),
			})
			return nil, fmt.Errorf("failed to create keyring directory: %w", err)
		}
	}

	// Process P2PConfig
	if config.P2PConfig.DataDir != "" {
		expandedDir := expandPath(config.P2PConfig.DataDir, homeDir)
		logtrace.Info(ctx, "Expanding P2P data directory", logtrace.Fields{
			"original": config.P2PConfig.DataDir,
			"expanded": expandedDir,
		})
		config.P2PConfig.DataDir = expandedDir

		if err := os.MkdirAll(config.P2PConfig.DataDir, 0700); err != nil {
			logtrace.Error(ctx, "Failed to create P2P data directory", logtrace.Fields{
				"dir":   config.P2PConfig.DataDir,
				"error": err.Error(),
			})
			return nil, fmt.Errorf("failed to create P2P data directory: %w", err)
		}
	}

	logtrace.Info(ctx, "Configuration loaded successfully", logtrace.Fields{
		"key_name":       config.SupernodeConfig.KeyName,
		"keyring_dir":    config.KeyringConfig.Dir,
		"p2p_listen":     config.P2PConfig.ListenAddress,
		"p2p_port":       config.P2PConfig.Port,
		"bootstrap":      config.P2PConfig.BootstrapNodes != "",
		"lumera_grpc":    config.LumeraClientConfig.GRPCAddr,
		"lumera_chainid": config.LumeraClientConfig.ChainID,
	})

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
