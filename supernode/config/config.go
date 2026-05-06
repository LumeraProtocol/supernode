package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	Port           uint16 `yaml:"port"`
	DataDir        string `yaml:"data_dir"`
	BootstrapNodes string `yaml:"bootstrap_nodes,omitempty"`
}

type LumeraClientConfig struct {
	GRPCAddr string `yaml:"grpc_addr"`
	ChainID  string `yaml:"chain_id"`

	// Optional tx/gas tuning knobs. Zero values fall back to the SDK
	// defaults (see pkg/lumera/modules/tx.Default* constants).
	//
	// gas_adjustment           multiplier applied to simulated gas
	//                          (default 1.3)
	// gas_adjustment_multiplier per-retry multiplier on OOG (default 1.3)
	// gas_adjustment_max_attempts total attempts on OOG (default 3, cap 10)
	// gas_padding              extra gas added on top of adjusted gas
	//                          (default 50000)
	// gas_price                "0.025" or "0.025ulume" (default "0.025")
	// fee_denom                coin denom for fees (default "ulume")
	GasAdjustment            float64 `yaml:"gas_adjustment,omitempty"`
	GasAdjustmentMultiplier  float64 `yaml:"gas_adjustment_multiplier,omitempty"`
	GasAdjustmentMaxAttempts int     `yaml:"gas_adjustment_max_attempts,omitempty"`
	GasPadding               uint64  `yaml:"gas_padding,omitempty"`
	GasPrice                 string  `yaml:"gas_price,omitempty"`
	FeeDenom                 string  `yaml:"fee_denom,omitempty"`
}

type RaptorQConfig struct {
	FilesDir string `yaml:"files_dir"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type StorageChallengeConfig struct {
	Enabled        bool                       `yaml:"enabled"`
	PollIntervalMs uint64                     `yaml:"poll_interval_ms,omitempty"`
	SubmitEvidence bool                       `yaml:"submit_evidence,omitempty"`
	LEP6           StorageChallengeLEP6Config `yaml:"lep6,omitempty"`
}

// StorageChallengeLEP6Config holds the supernode-binary-owned knobs for
// the LEP-6 compound storage challenge runtime. All chain-driven knobs
// (bucket thresholds, ranges-per-artifact, range size, enforcement mode)
// flow via x/audit Params and are deliberately omitted here. See
// docs/plans/LEP6_SUPERNODE_IMPLEMENTATION_PLAN_v2.md §2.3.
type StorageChallengeLEP6Config struct {
	// enabledSet tracks whether YAML explicitly provided enabled. Plain bools
	// cannot distinguish omitted from explicit false, but LEP-6 needs both safe
	// default-on local toggles and emergency-disable `enabled: false`.
	enabledSet bool `yaml:"-"`

	// Enabled gates construction of the LEP6Dispatcher. When false, the
	// legacy single-range loop runs alone (default true; the chain audit
	// StorageTruthEnforcementMode remains the protocol source of truth).
	Enabled bool `yaml:"enabled"`
	// MaxConcurrentTargets bounds parallelism inside DispatchEpoch.
	// Default 4. Reserved for follow-up parallelism work; PR3 dispatch
	// is currently sequential per target.
	MaxConcurrentTargets int `yaml:"max_concurrent_targets,omitempty"`
	// RecipientReadTimeout caps a single GetCompoundProof RPC. Default
	// 30s.
	RecipientReadTimeout time.Duration `yaml:"recipient_read_timeout,omitempty"`
	// Recheck owns the PR-5 storage-truth recheck evidence submitter.
	Recheck StorageRecheckConfig `yaml:"recheck,omitempty"`
}

type StorageRecheckConfig struct {
	enabledSet bool `yaml:"-"`

	Enabled        bool   `yaml:"enabled"`
	LookbackEpochs uint64 `yaml:"lookback_epochs,omitempty"`
	MaxPerTick     int    `yaml:"max_per_tick,omitempty"`
	TickIntervalMs int    `yaml:"tick_interval_ms,omitempty"`
	// MaxFailureAttemptsPerTicket bounds repeated failed recheck attempts for
	// one epoch/ticket before the candidate is temporarily skipped.
	MaxFailureAttemptsPerTicket int `yaml:"max_failure_attempts_per_ticket,omitempty"`
	// FailureBackoffTTLms is the TTL for recorded recheck attempt failures.
	FailureBackoffTTLms int `yaml:"failure_backoff_ttl_ms,omitempty"`
}

// SelfHealingConfig configures the LEP-6 chain-driven self-healing runtime
// (supernode/self_healing). Mode gating is also enforced at runtime via
// the chain's StorageTruthEnforcementMode param — UNSPECIFIED skips the
// dispatcher regardless of Enabled.
type SelfHealingConfig struct {
	// enabledSet tracks explicit YAML emergency-disable vs omitted default.
	enabledSet bool `yaml:"-"`

	// Enabled toggles the dispatcher and the §19 transport server. Default
	// true; chain StorageTruthEnforcementMode=UNSPECIFIED remains the global
	// protocol disable.
	Enabled bool `yaml:"enabled"`
	// PollIntervalMs is the dispatcher tick cadence (default 30000).
	PollIntervalMs int `yaml:"poll_interval_ms,omitempty"`
	// MaxConcurrentReconstructs bounds RaptorQ reseeds (RAM-heavy).
	// Default 2.
	MaxConcurrentReconstructs int `yaml:"max_concurrent_reconstructs,omitempty"`
	// MaxConcurrentVerifications bounds verifier fetch+hash workers.
	// Default 4.
	MaxConcurrentVerifications int `yaml:"max_concurrent_verifications,omitempty"`
	// MaxConcurrentPublishes bounds publish-to-KAD workers. Default 2.
	MaxConcurrentPublishes int `yaml:"max_concurrent_publishes,omitempty"`
	// StagingDir is the local staging root (default ~/.supernode/heal-staging).
	StagingDir string `yaml:"staging_dir,omitempty"`
	// VerifierFetchTimeoutMs caps a single ServeReconstructedArtefacts
	// stream from healer (default 60000).
	VerifierFetchTimeoutMs int `yaml:"verifier_fetch_timeout_ms,omitempty"`
	// VerifierFetchAttempts bounds retries when fetching from healer
	// (default 3).
	VerifierFetchAttempts int `yaml:"verifier_fetch_attempts,omitempty"`
	// VerifierBackoffBaseMs is the exponential retry backoff base between
	// healer fetch attempts (default 2000).
	VerifierBackoffBaseMs int `yaml:"verifier_backoff_base_ms,omitempty"`
	// AuditQueryTimeoutMs bounds each dispatcher chain query so one wedged
	// status/params call cannot starve verifier/finalizer work (default 10000).
	AuditQueryTimeoutMs int `yaml:"audit_query_timeout_ms,omitempty"`
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
	if err := config.applyLEP6DefaultsAndValidate(); err != nil {
		return nil, err
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
