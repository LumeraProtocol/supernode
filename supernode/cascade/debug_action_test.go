package cascade

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

// debugActionID is intentionally a constant so it is easy to change
// and re-run this helper test from VS Code for different actions.
// Set it to a real on-chain Cascade action ID before running.
const debugActionID = "10113"

// TestDebugReverseEngineerAction is a convenience wrapper around
// DebugReverseEngineerAction. It is not a real unit test; it simply
// wires up configuration and logs a detailed breakdown for a single
// action ID, making it easy to inspect from VS Code.
func TestDebugReverseEngineerAction(t *testing.T) {
	if debugActionID == "" {
		t.Skip("set debugActionID to a real action ID to run this debug helper")
	}

	// Initialize Cosmos SDK config (Bech32 prefixes, etc.).
	keyring.InitSDKConfig()

	// Use the same logging setup as the supernode binary for consistent output.
	logtrace.Setup("supernode-debug")

	ctx := context.Background()

	// Derive base directory and config path as in the supernode CLI.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home directory: %v", err)
	}
	baseDir := filepath.Join(homeDir, ".supernode")
	cfgFile := filepath.Join(baseDir, "config.yml")

	cfg, err := config.LoadConfig(cfgFile, baseDir)
	if err != nil {
		t.Fatalf("failed to load supernode config from %s: %v", cfgFile, err)
	}

	// Initialize keyring using the configured directory.
	keyringCfg := cfg.KeyringConfig
	keyringCfg.Dir = cfg.GetKeyringDir()

	kr, err := keyring.InitKeyring(keyringCfg)
	if err != nil {
		t.Fatalf("failed to initialize keyring: %v", err)
	}

	// Initialize Lumera client using the same configuration as the supernode.
	lumeraCfg, err := lumera.NewConfig(
		cfg.LumeraClientConfig.GRPCAddr,
		cfg.LumeraClientConfig.ChainID,
		cfg.SupernodeConfig.KeyName,
		kr,
	)
	if err != nil {
		t.Fatalf("failed to create Lumera config: %v", err)
	}

	lumeraClient, err := lumera.NewClient(ctx, lumeraCfg)
	if err != nil {
		t.Fatalf("failed to create Lumera client: %v", err)
	}
	defer func() {
		_ = lumeraClient.Close()
	}()

	// We only need the Lumera client for this debug helper; P2P and codec
	// are left nil because DebugReverseEngineerAction is read-only and
	// does not depend on them.
	service := NewCascadeService(
		cfg.SupernodeConfig.Identity,
		lumeraClient,
		nil, // p2p.Client
		nil, // codec.Codec
		nil, // rqstore.Store
	)

	task := NewCascadeRegistrationTask(service)

	if err := task.DebugReverseEngineerAction(ctx, debugActionID); err != nil {
		t.Fatalf("DebugReverseEngineerAction returned error: %v", err)
	}
}
