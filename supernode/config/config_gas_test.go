package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig_GasTuningKnobs verifies the new optional tx/gas YAML knobs
// round-trip correctly through LoadConfig and are exposed on LumeraClientConfig.
func TestLoadConfig_GasTuningKnobs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "supernode.yml")

	const yamlBody = `
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
  gas_adjustment: 1.15
  gas_adjustment_multiplier: 1.5
  gas_adjustment_max_attempts: 4
  gas_padding: 123456
  gas_price: "0.030"
  fee_denom: ulume
raptorq:
  files_dir: raptorq_files
storage_challenge:
  enabled: true
  poll_interval_ms: 1000
  submit_evidence: false
`
	if err := os.WriteFile(path, []byte(yamlBody), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	got := cfg.LumeraClientConfig
	if got.GasAdjustment != 1.15 {
		t.Errorf("GasAdjustment = %v, want 1.15", got.GasAdjustment)
	}
	if got.GasAdjustmentMultiplier != 1.5 {
		t.Errorf("GasAdjustmentMultiplier = %v, want 1.5", got.GasAdjustmentMultiplier)
	}
	if got.GasAdjustmentMaxAttempts != 4 {
		t.Errorf("GasAdjustmentMaxAttempts = %v, want 4", got.GasAdjustmentMaxAttempts)
	}
	if got.GasPadding != 123456 {
		t.Errorf("GasPadding = %v, want 123456", got.GasPadding)
	}
	if got.GasPrice != "0.030" {
		t.Errorf("GasPrice = %q, want 0.030", got.GasPrice)
	}
	if got.FeeDenom != "ulume" {
		t.Errorf("FeeDenom = %q, want ulume", got.FeeDenom)
	}
}

// TestLoadConfig_GasTuningKnobs_ZeroMeansDefault verifies that omitting the
// optional gas knobs yields zero values on the struct (caller falls back to
// package defaults inside pkg/lumera/modules/tx).
func TestLoadConfig_GasTuningKnobs_ZeroMeansDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "supernode.yml")

	const yamlBody = `
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
`
	if err := os.WriteFile(path, []byte(yamlBody), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	got := cfg.LumeraClientConfig
	if got.GasAdjustment != 0 {
		t.Errorf("GasAdjustment = %v, want 0 (omitted → default)", got.GasAdjustment)
	}
	if got.GasAdjustmentMultiplier != 0 {
		t.Errorf("GasAdjustmentMultiplier = %v, want 0", got.GasAdjustmentMultiplier)
	}
	if got.GasAdjustmentMaxAttempts != 0 {
		t.Errorf("GasAdjustmentMaxAttempts = %v, want 0", got.GasAdjustmentMaxAttempts)
	}
	if got.GasPadding != 0 {
		t.Errorf("GasPadding = %v, want 0", got.GasPadding)
	}
	if got.GasPrice != "" {
		t.Errorf("GasPrice = %q, want empty", got.GasPrice)
	}
	if got.FeeDenom != "" {
		t.Errorf("FeeDenom = %q, want empty", got.FeeDenom)
	}
}
