package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig_LEP6SafeDefaults(t *testing.T) {
	t.Parallel()

	cfg := loadConfigFromBody(t, `
supernode:
  key_name: test-key
  identity: lumera1identity000000000000000000000000000000
  host: 0.0.0.0
  port: 4444
keyring:
  backend: test
  dir: keys
p2p:
  port: 4445
  data_dir: data/p2p
lumera:
  grpc_addr: localhost:9090
  chain_id: testing
raptorq:
  files_dir: raptorq_files
storage_challenge:
  enabled: true
`)

	if !cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("storage_challenge.lep6.enabled default = false, want true so chain mode remains protocol source of truth")
	}
	if cfg.StorageChallengeConfig.LEP6.MaxConcurrentTargets != DefaultLEP6MaxConcurrentTargets {
		t.Fatalf("max_concurrent_targets = %d, want %d", cfg.StorageChallengeConfig.LEP6.MaxConcurrentTargets, DefaultLEP6MaxConcurrentTargets)
	}
	if cfg.StorageChallengeConfig.LEP6.RecipientReadTimeout != DefaultLEP6RecipientReadTimeout {
		t.Fatalf("recipient_read_timeout = %s, want %s", cfg.StorageChallengeConfig.LEP6.RecipientReadTimeout, DefaultLEP6RecipientReadTimeout)
	}
	if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("storage_challenge.lep6.recheck.enabled default = false, want true")
	}
	if cfg.StorageChallengeConfig.LEP6.Recheck.LookbackEpochs != DefaultLEP6RecheckLookbackEpochs {
		t.Fatalf("recheck.lookback_epochs = %d, want %d", cfg.StorageChallengeConfig.LEP6.Recheck.LookbackEpochs, DefaultLEP6RecheckLookbackEpochs)
	}
	if cfg.StorageChallengeConfig.LEP6.Recheck.MaxPerTick != DefaultLEP6RecheckMaxPerTick {
		t.Fatalf("recheck.max_per_tick = %d, want %d", cfg.StorageChallengeConfig.LEP6.Recheck.MaxPerTick, DefaultLEP6RecheckMaxPerTick)
	}
	if cfg.StorageChallengeConfig.LEP6.Recheck.TickIntervalMs != int(DefaultLEP6RecheckTickInterval/time.Millisecond) {
		t.Fatalf("recheck.tick_interval_ms = %d, want %d", cfg.StorageChallengeConfig.LEP6.Recheck.TickIntervalMs, int(DefaultLEP6RecheckTickInterval/time.Millisecond))
	}
	if cfg.StorageChallengeConfig.LEP6.Recheck.MaxFailureAttemptsPerTicket != DefaultLEP6RecheckMaxFailureAttemptsPerTicket {
		t.Fatalf("recheck.max_failure_attempts_per_ticket = %d, want %d", cfg.StorageChallengeConfig.LEP6.Recheck.MaxFailureAttemptsPerTicket, DefaultLEP6RecheckMaxFailureAttemptsPerTicket)
	}
	if cfg.StorageChallengeConfig.LEP6.Recheck.FailureBackoffTTLms != int(DefaultLEP6RecheckFailureBackoffTTL/time.Millisecond) {
		t.Fatalf("recheck.failure_backoff_ttl_ms = %d, want %d", cfg.StorageChallengeConfig.LEP6.Recheck.FailureBackoffTTLms, int(DefaultLEP6RecheckFailureBackoffTTL/time.Millisecond))
	}

	if !cfg.SelfHealingConfig.Enabled {
		t.Fatalf("self_healing.enabled default = false, want true so chain UNSPECIFIED is the global protocol gate")
	}
	if cfg.SelfHealingConfig.PollIntervalMs != int(DefaultSelfHealingPollInterval/time.Millisecond) {
		t.Fatalf("self_healing.poll_interval_ms = %d, want %d", cfg.SelfHealingConfig.PollIntervalMs, int(DefaultSelfHealingPollInterval/time.Millisecond))
	}
	if cfg.SelfHealingConfig.MaxConcurrentReconstructs != DefaultSelfHealingMaxConcurrentReconstructs {
		t.Fatalf("self_healing.max_concurrent_reconstructs = %d, want %d", cfg.SelfHealingConfig.MaxConcurrentReconstructs, DefaultSelfHealingMaxConcurrentReconstructs)
	}
	if cfg.SelfHealingConfig.MaxConcurrentVerifications != DefaultSelfHealingMaxConcurrentVerifications {
		t.Fatalf("self_healing.max_concurrent_verifications = %d, want %d", cfg.SelfHealingConfig.MaxConcurrentVerifications, DefaultSelfHealingMaxConcurrentVerifications)
	}
	if cfg.SelfHealingConfig.MaxConcurrentPublishes != DefaultSelfHealingMaxConcurrentPublishes {
		t.Fatalf("self_healing.max_concurrent_publishes = %d, want %d", cfg.SelfHealingConfig.MaxConcurrentPublishes, DefaultSelfHealingMaxConcurrentPublishes)
	}
	if cfg.SelfHealingConfig.StagingDir != DefaultSelfHealingStagingDir {
		t.Fatalf("self_healing.staging_dir = %q, want %q", cfg.SelfHealingConfig.StagingDir, DefaultSelfHealingStagingDir)
	}
	if cfg.SelfHealingConfig.VerifierFetchTimeoutMs != int(DefaultSelfHealingVerifierFetchTimeout/time.Millisecond) {
		t.Fatalf("self_healing.verifier_fetch_timeout_ms = %d, want %d", cfg.SelfHealingConfig.VerifierFetchTimeoutMs, int(DefaultSelfHealingVerifierFetchTimeout/time.Millisecond))
	}
	if cfg.SelfHealingConfig.VerifierFetchAttempts != DefaultSelfHealingVerifierFetchAttempts {
		t.Fatalf("self_healing.verifier_fetch_attempts = %d, want %d", cfg.SelfHealingConfig.VerifierFetchAttempts, DefaultSelfHealingVerifierFetchAttempts)
	}
	if cfg.SelfHealingConfig.VerifierBackoffBaseMs != int(DefaultSelfHealingVerifierBackoffBase/time.Millisecond) {
		t.Fatalf("self_healing.verifier_backoff_base_ms = %d, want %d", cfg.SelfHealingConfig.VerifierBackoffBaseMs, int(DefaultSelfHealingVerifierBackoffBase/time.Millisecond))
	}
}

func TestLoadConfig_LEP6EmergencyDisablesRemainFalse(t *testing.T) {
	t.Parallel()

	cfg := loadConfigFromBody(t, `
supernode:
  key_name: test-key
  identity: lumera1identity000000000000000000000000000000
  host: 0.0.0.0
  port: 4444
keyring:
  backend: test
  dir: keys
p2p:
  port: 4445
  data_dir: data/p2p
lumera:
  grpc_addr: localhost:9090
  chain_id: testing
raptorq:
  files_dir: raptorq_files
storage_challenge:
  enabled: true
  lep6:
    enabled: false
    recheck:
      enabled: false
self_healing:
  enabled: false
`)

	if cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("storage_challenge.lep6.enabled = true, want explicit false emergency disable preserved")
	}
	if cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("storage_challenge.lep6.recheck.enabled = true, want explicit false emergency disable preserved")
	}
	if cfg.SelfHealingConfig.Enabled {
		t.Fatalf("self_healing.enabled = true, want explicit false emergency disable preserved")
	}
}

func TestLoadConfig_LEP6InvalidNegativeKnobsRejected(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"dispatcher-targets": "storage_challenge:\n  enabled: true\n  lep6:\n    max_concurrent_targets: -1\n",
		"dispatcher-timeout": "storage_challenge:\n  enabled: true\n  lep6:\n    recipient_read_timeout: -1s\n",
		"recheck-max":        "storage_challenge:\n  enabled: true\n  lep6:\n    recheck:\n      max_per_tick: -1\n",
		"recheck-ttl":        "storage_challenge:\n  enabled: true\n  lep6:\n    recheck:\n      failure_backoff_ttl_ms: -1\n",
		"healing-poll":       "storage_challenge:\n  enabled: true\nself_healing:\n  poll_interval_ms: -1\n",
		"healing-backoff":    "storage_challenge:\n  enabled: true\nself_healing:\n  verifier_backoff_base_ms: -1\n",
	}

	for name, override := range cases {
		name, override := name, override
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := baseConfigYAML() + override
			dir := t.TempDir()
			path := filepath.Join(dir, "supernode.yml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write yaml: %v", err)
			}
			_, err := LoadConfig(path, dir)
			if err == nil {
				t.Fatalf("LoadConfig succeeded, want validation error")
			}
			if !strings.Contains(err.Error(), "LEP-6") {
				t.Fatalf("error = %v, want LEP-6 validation context", err)
			}
		})
	}
}

func TestCreateDefaultConfig_IncludesExplicitLEP6Blocks(t *testing.T) {
	t.Parallel()

	cfg := CreateDefaultConfig("test-key", "lumera1identity", "testing", "test", "keys", "", "", "")
	if !cfg.StorageChallengeConfig.LEP6.Enabled || !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled || !cfg.SelfHealingConfig.Enabled {
		t.Fatalf("default config should explicitly include enabled LEP-6 local toggles behind chain mode gate: %+v", cfg)
	}
	if cfg.SelfHealingConfig.StagingDir == "" {
		t.Fatalf("default config missing self_healing.staging_dir")
	}
}

func TestSystemConfigFixturesIncludeLEP6(t *testing.T) {
	t.Parallel()

	fixtures := []string{
		"../../tests/system/config.lep6-1.yml",
		"../../tests/system/config.lep6-2.yml",
		"../../tests/system/config.lep6-3.yml",
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			body := string(raw)
			for _, want := range []string{"storage_challenge:", "lep6:", "recheck:", "self_healing:"} {
				if !strings.Contains(body, want) {
					t.Fatalf("fixture %s missing %q", fixture, want)
				}
			}
			cfg, err := LoadConfig(fixture, t.TempDir())
			if err != nil {
				t.Fatalf("LoadConfig(%s): %v", fixture, err)
			}
			if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled || !cfg.SelfHealingConfig.Enabled {
				t.Fatalf("fixture should enable LEP-6 recheck/self-healing runtimes behind chain mode gate: %+v", cfg)
			}
		})
	}
}

func loadConfigFromBody(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "supernode.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func baseConfigYAML() string {
	return `
supernode:
  key_name: test-key
  identity: lumera1identity000000000000000000000000000000
  host: 0.0.0.0
  port: 4444
keyring:
  backend: test
  dir: keys
p2p:
  port: 4445
  data_dir: data/p2p
lumera:
  grpc_addr: localhost:9090
  chain_id: testing
raptorq:
  files_dir: raptorq_files
`
}
