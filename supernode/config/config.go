package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"gopkg.in/yaml.v3"
)

// Config represents the YAML configuration structure
type Config struct {
	SupernodeID string `yaml:"supernode_id"`

	Keyring struct {
		Backend  string `yaml:"backend"`
		Dir      string `yaml:"dir"`
		Password string `yaml:"password"`
	} `yaml:"keyring"`

	P2P struct {
		ListenAddress  string `yaml:"listen_address"`
		Port           uint16 `yaml:"port"`
		DataDir        string `yaml:"data_dir"`
		BootstrapNodes string `yaml:"bootstrap_nodes"`
		ExternalIP     string `yaml:"external_ip"`
	} `yaml:"p2p"`

	Lumera struct {
		GRPCAddr string `yaml:"grpc_addr"`
		ChainID  string `yaml:"chain_id"`
		Timeout  int    `yaml:"timeout"`
	} `yaml:"lumera"`
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

	// Set default values if not provided
	if config.SupernodeID == "" {
		return nil, fmt.Errorf("supernode_id is required in config file")
	}

	// Set defaults for P2P
	if config.P2P.ListenAddress == "" {
		config.P2P.ListenAddress = "0.0.0.0"
		logtrace.Info(ctx, "Using default P2P listen address", logtrace.Fields{
			"address": config.P2P.ListenAddress,
		})
	}

	if config.P2P.Port == 0 {
		config.P2P.Port = 4445
		logtrace.Info(ctx, "Using default P2P port", logtrace.Fields{
			"port": config.P2P.Port,
		})
	}

	if config.P2P.DataDir == "" {
		config.P2P.DataDir = "./data/p2p"
		logtrace.Info(ctx, "Using default P2P data directory", logtrace.Fields{
			"dir": config.P2P.DataDir,
		})
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(config.P2P.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create P2P data directory: %w", err)
	}

	// Set defaults for Keyring
	if config.Keyring.Backend == "" {
		config.Keyring.Backend = "file"
		logtrace.Info(ctx, "Using default keyring backend", logtrace.Fields{
			"backend": config.Keyring.Backend,
		})
	}

	if config.Keyring.Dir == "" {
		config.Keyring.Dir = "./keys"
		logtrace.Info(ctx, "Using default keyring directory", logtrace.Fields{
			"dir": config.Keyring.Dir,
		})
	}

	// Create keyring directory if it doesn't exist
	if err := os.MkdirAll(config.Keyring.Dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create keyring directory: %w", err)
	}

	// Set defaults for Lumera
	if config.Lumera.GRPCAddr == "" {
		config.Lumera.GRPCAddr = "localhost:9090"
		logtrace.Info(ctx, "Using default Lumera gRPC address", logtrace.Fields{
			"address": config.Lumera.GRPCAddr,
		})
	}

	if config.Lumera.ChainID == "" {
		config.Lumera.ChainID = "lumera"
		logtrace.Info(ctx, "Using default Lumera chain ID", logtrace.Fields{
			"chain_id": config.Lumera.ChainID,
		})
	}

	if config.Lumera.Timeout <= 0 {
		config.Lumera.Timeout = 10
		logtrace.Info(ctx, "Using default Lumera timeout", logtrace.Fields{
			"timeout": config.Lumera.Timeout,
		})
	}

	logtrace.Info(ctx, "Configuration loaded successfully", logtrace.Fields{})
	return &config, nil
}
