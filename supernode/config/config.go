package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"gopkg.in/yaml.v3"
)

type SupernodeConfig struct {
	KeyName  string `yaml:"key_name"`
	Identity string `yaml:"identity"`
	Host     string `yaml:"host"`
	// IPAddress is an accepted alias for Host to support older configs
	IPAddress   string `yaml:"ip_address,omitempty"`
	Port        uint16 `yaml:"port"`
	GatewayPort uint16 `yaml:"gateway_port,omitempty"`
}

type KeyringConfig struct {
	Backend   string `yaml:"backend,omitempty"`
	Dir       string `yaml:"dir,omitempty"`
	PassPlain string `yaml:"passphrase_plain,omitempty"`
	PassEnv   string `yaml:"passphrase_env,omitempty"`
	PassFile  string `yaml:"passphrase_file,omitempty"`
}

type P2PConfig struct {
	Port    uint16 `yaml:"port"`
	DataDir string `yaml:"data_dir"`
}

type LumeraClientConfig struct {
	GRPCAddr string `yaml:"grpc_addr"`
	ChainID  string `yaml:"chain_id"`
}

type RaptorQConfig struct {
	FilesDir string `yaml:"files_dir"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type StorageChallengeConfig struct {
	Enabled        bool   `yaml:"enabled"`
	PollIntervalMs uint64 `yaml:"poll_interval_ms,omitempty"`
	SubmitEvidence bool   `yaml:"submit_evidence,omitempty"`
}

type SelfHealingConfig struct {
	Enabled                 bool   `yaml:"enabled"`
	PollIntervalMs          uint64 `yaml:"poll_interval_ms,omitempty"`
	ActionPageLimit         uint64 `yaml:"action_page_limit,omitempty"`
	ActionTargetsTTLSeconds uint64 `yaml:"action_targets_ttl_seconds,omitempty"`
	MaxChallenges           uint64 `yaml:"max_challenges,omitempty"`
	MaxEventsPerTick        uint64 `yaml:"max_events_per_tick,omitempty"`
	EventWorkers            uint64 `yaml:"event_workers,omitempty"`
	EventLeaseDurationMs    uint64 `yaml:"event_lease_duration_ms,omitempty"`
	EventRetryBaseMs        uint64 `yaml:"event_retry_base_ms,omitempty"`
	EventRetryMaxMs         uint64 `yaml:"event_retry_max_ms,omitempty"`
	MaxEventAttempts        uint64 `yaml:"max_event_attempts,omitempty"`
	MaxWindowAgeMs          uint64 `yaml:"max_window_age_ms,omitempty"`
}

type Config struct {
	SupernodeConfig        `yaml:"supernode"`
	KeyringConfig          `yaml:"keyring"`
	P2PConfig              `yaml:"p2p"`
	LumeraClientConfig     `yaml:"lumera"`
	RaptorQConfig          `yaml:"raptorq"`
	StorageChallengeConfig `yaml:"storage_challenge"`
	SelfHealingConfig      `yaml:"self_healing"`

	// Store base directory (not from YAML)
	BaseDir string `yaml:"-"`
}

// GetFullPath returns the absolute path by combining base directory with relative path
// If the path is already absolute, it returns the path as-is
func (c *Config) GetFullPath(relativePath string) string {
	if relativePath == "" {
		return c.BaseDir
	}
	if filepath.IsAbs(relativePath) {
		return relativePath
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

	// Support both 'host' and legacy 'ip_address' fields. If 'host' is empty
	// and 'ip_address' is provided, use it as the host value.
	if strings.TrimSpace(config.SupernodeConfig.Host) == "" && strings.TrimSpace(config.SupernodeConfig.IPAddress) != "" {
		config.SupernodeConfig.Host = strings.TrimSpace(config.SupernodeConfig.IPAddress)
		logtrace.Debug(ctx, "Using ip_address as host", logtrace.Fields{
			"ip_address": config.SupernodeConfig.IPAddress,
		})
	}

	// Set the base directory
	config.BaseDir = baseDir

	// Apply storage challenge defaults.
	if config.StorageChallengeConfig.PollIntervalMs == 0 {
		config.StorageChallengeConfig.PollIntervalMs = DefaultStorageChallengePollIntervalMs
	}
	if config.SelfHealingConfig.PollIntervalMs == 0 {
		config.SelfHealingConfig.PollIntervalMs = DefaultSelfHealingPollIntervalMs
	}

	// Create directories
	if err := config.EnsureDirs(); err != nil {
		return nil, err
	}

	logtrace.Debug(ctx, "Configuration loaded successfully", logtrace.Fields{
		"baseDir":         baseDir,
		"keyringDir":      config.GetKeyringDir(),
		"p2pDataDir":      config.GetP2PDataDir(),
		"raptorqFilesDir": config.GetRaptorQFilesDir(),
	})

	return &config, nil
}
