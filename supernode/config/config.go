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
	Identity  string `yaml:"identity"`
	IpAddress string `yaml:"ip_address"`
	Port      uint16 `yaml:"port"`
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

type RaptorQConfig struct {
	ServiceAddress string `yaml:"service_address"`
	FilesDir       string `yaml:"files_dir"`
}

type Config struct {
	SupernodeConfig    `yaml:"supernode"`
	KeyringConfig      `yaml:"keyring"`
	P2PConfig          `yaml:"p2p"`
	LumeraClientConfig `yaml:"lumera"`
	RaptorQConfig      `yaml:"raptorq"`

	// Store base directory (not from YAML)
	BaseDir string `yaml:"-"`
}

// GetFullPath returns the absolute path by combining base directory with relative path
func (c *Config) GetFullPath(relativePath string) string {
	if relativePath == "" {
		return c.BaseDir
	}
	return filepath.Join(c.BaseDir, relativePath)
}

// GetKeyringDir returns the full path to the keyring directory
func (c *Config) GetKeyringDir() string {
	return c.GetFullPath(c.KeyringConfig.Dir)
}

// GetP2PDataDir returns the full path to the P2P data directory
func (c *Config) GetP2PDataDir() string {
	return c.GetFullPath(c.P2PConfig.DataDir)
}

// GetRaptorQFilesDir returns the full path to the RaptorQ files directory
func (c *Config) GetRaptorQFilesDir() string {
	return c.GetFullPath(c.RaptorQConfig.FilesDir)
}

// GetAllDirs returns all configured directories
func (c *Config) GetAllDirs() map[string]string {
	return map[string]string{
		"base":    c.BaseDir,
		"keyring": c.GetKeyringDir(),
		"p2p":     c.GetP2PDataDir(),
		"raptorq": c.GetRaptorQFilesDir(),
	}
}

// EnsureDirs creates all required directories
func (c *Config) EnsureDirs() error {
	dirs := c.GetAllDirs()
	for name, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create %s directory at %s: %w", name, dir, err)
		}
	}
	return nil
}

// LoadConfig loads the configuration from a file and applies the base directory
func LoadConfig(filename string, baseDir string) (*Config, error) {
	ctx := context.Background()

	// Check if config file exists
	absPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("error getting absolute path for config file: %w", err)
	}

	logtrace.Info(ctx, "Loading configuration", logtrace.Fields{
		"path":    absPath,
		"baseDir": baseDir,
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

	// Set the base directory
	config.BaseDir = baseDir

	// Create directories
	if err := config.EnsureDirs(); err != nil {
		return nil, err
	}

	logtrace.Info(ctx, "Configuration loaded successfully", logtrace.Fields{
		"baseDir":         baseDir,
		"keyringDir":      config.GetKeyringDir(),
		"p2pDataDir":      config.GetP2PDataDir(),
		"raptorqFilesDir": config.GetRaptorQFilesDir(),
	})

	return &config, nil
}
