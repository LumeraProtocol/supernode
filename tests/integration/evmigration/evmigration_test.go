// Package evmigration provides integration tests for the EVM account migration flow.
// These tests exercise the full keyring lifecycle (legacy key detection → EVM key
// validation → key delete → config save) without a live chain.
package evmigration

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	snConfig "github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	evmcryptocodec "github.com/cosmos/evm/crypto/codec"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	evmhd "github.com/cosmos/evm/crypto/hd"
	"github.com/cosmos/go-bip39"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newTestKeyring creates an in-memory keyring supporting both key algorithms.
func newTestKeyring(t *testing.T) sdkkeyring.Keyring {
	t.Helper()
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	evmcryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	return sdkkeyring.NewInMemory(cdc, evmhd.EthSecp256k1Option())
}

func generateMnemonic(t *testing.T) string {
	t.Helper()
	entropy, err := bip39.NewEntropy(256)
	require.NoError(t, err)
	mn, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)
	return mn
}

// TestFullMigrationKeyringFlow tests the complete keyring migration lifecycle:
// 1. Start with a legacy secp256k1 key
// 2. Import an EVM key from the same mnemonic
// 3. Sign migration payload with both keys (dual signing)
// 4. Delete legacy key (EVM key stays under its name; config key_name is updated)
// 5. Verify the EVM key is accessible and legacy key is gone
func TestFullMigrationKeyringFlow(t *testing.T) {
	kr := newTestKeyring(t)
	mnemonic := generateMnemonic(t)

	// Step 1: Import legacy key (secp256k1, coin type 118).
	legacyRec, err := kr.NewAccount("mykey", mnemonic, "", "m/44'/118'/0'/0/0", hd.Secp256k1)
	require.NoError(t, err)
	legacyAddr, err := legacyRec.GetAddress()
	require.NoError(t, err)
	legacyPubKey, err := legacyRec.GetPubKey()
	require.NoError(t, err)

	_, isLegacy := legacyPubKey.(*secp256k1.PubKey)
	require.True(t, isLegacy, "initial key should be secp256k1")

	// Step 2: Import EVM key (eth_secp256k1, coin type 60) from same mnemonic.
	evmRec, err := kr.NewAccount("evm-key", mnemonic, "", "m/44'/60'/0'/0/0", evmhd.EthSecp256k1)
	require.NoError(t, err)
	evmPubKey, err := evmRec.GetPubKey()
	require.NoError(t, err)
	evmAddr := sdk.AccAddress(evmPubKey.Address())

	_, isEVM := evmPubKey.(*ethsecp256k1.PubKey)
	require.True(t, isEVM, "EVM key should be eth_secp256k1")

	// Addresses must differ.
	require.NotEqual(t, legacyAddr.String(), evmAddr.String(),
		"legacy and EVM keys should have different addresses")

	t.Logf("Legacy address: %s", legacyAddr.String())
	t.Logf("EVM address:    %s", evmAddr.String())

	// Step 3: Sign migration payload with both keys (simulating what
	// ensureLegacyAccountMigrated does).
	payload := []byte("lumera-evm-migration:test-chain-1:76857769:claim:" + legacyAddr.String() + ":" + evmAddr.String())

	// Legacy signing: SHA256(payload) → keyring.Sign (which does another SHA256 internally).
	hash := sha256.Sum256(payload)
	legacySig, _, err := kr.Sign("mykey", hash[:], signingtypes.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	require.NotEmpty(t, legacySig)

	// EVM signing: raw payload → keyring.Sign (eth_secp256k1 uses Keccak-256 internally).
	evmSig, _, err := kr.Sign("evm-key", payload, signingtypes.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	require.NotEmpty(t, evmSig)

	// Verify legacy signature against the public key.
	// The chain verifier does: VerifySignature(SHA256(payload), sig) which internally
	// SHA256s again. We replicate that here.
	verified := legacyPubKey.VerifySignature(hash[:], legacySig)
	assert.True(t, verified, "legacy signature should verify")

	// Verify EVM signature.
	verified = evmPubKey.VerifySignature(payload, evmSig)
	assert.True(t, verified, "EVM signature should verify")

	// Step 4: Delete legacy key. EVM key stays under "evm-key";
	// config key_name would be updated to "evm-key" in the real flow.
	require.NoError(t, kr.Delete("mykey"))

	// Step 5: Verify legacy key is gone, EVM key is accessible.
	_, err = kr.Key("mykey")
	assert.Error(t, err, "legacy key should be deleted")

	finalRec, err := kr.Key("evm-key")
	require.NoError(t, err)
	finalPubKey, err := finalRec.GetPubKey()
	require.NoError(t, err)

	_, isEVMFinal := finalPubKey.(*ethsecp256k1.PubKey)
	assert.True(t, isEVMFinal, "EVM key should still be eth_secp256k1")

	finalAddr, err := finalRec.GetAddress()
	require.NoError(t, err)
	assert.Equal(t, evmAddr.String(), finalAddr.String(),
		"EVM key should have the same address")
}

// TestConfigPersistenceAfterMigration verifies that identity and evm_key_name
// are correctly persisted/cleared in config.yaml after migration.
func TestConfigPersistenceAfterMigration(t *testing.T) {
	tmpDir := t.TempDir()

	// Create initial config with legacy identity and evm_key_name set.
	cfg := &snConfig.Config{
		SupernodeConfig: snConfig.SupernodeConfig{
			KeyName:    "mykey",
			Identity:   "lumera1legacyaddr123",
			Host:       "127.0.0.1",
			Port:       4444,
			EVMKeyName: "evm-key",
		},
		KeyringConfig: snConfig.KeyringConfig{
			Backend: "test",
			Dir:     "keyring",
		},
		P2PConfig: snConfig.P2PConfig{
			Port:    4445,
			DataDir: "data/p2p",
		},
		LumeraClientConfig: snConfig.LumeraClientConfig{
			GRPCAddr: "localhost:9090",
			ChainID:  "lumera-testnet",
		},
		RaptorQConfig: snConfig.RaptorQConfig{
			FilesDir: "data/raptorq",
		},
	}
	cfg.BaseDir = tmpDir

	cfgFile := filepath.Join(tmpDir, "config.yml")
	require.NoError(t, snConfig.SaveConfig(cfg, cfgFile))

	// Simulate migration: key_name → evm key, update identity, clear evm_key_name.
	newAddr := "lumera1newevmaddr456"
	cfg.SupernodeConfig.KeyName = "evm-key"
	cfg.SupernodeConfig.Identity = newAddr
	cfg.SupernodeConfig.EVMKeyName = ""
	require.NoError(t, snConfig.SaveConfig(cfg, cfgFile))

	// Reload config and verify.
	loaded, err := snConfig.LoadConfig(cfgFile, tmpDir)
	require.NoError(t, err)

	assert.Equal(t, "evm-key", loaded.SupernodeConfig.KeyName,
		"key_name should be updated to EVM key name")
	assert.Equal(t, newAddr, loaded.SupernodeConfig.Identity,
		"identity should be updated to new EVM address")
	assert.Empty(t, loaded.SupernodeConfig.EVMKeyName,
		"evm_key_name should be cleared after migration")
	assert.Equal(t, "lumera-testnet", loaded.LumeraClientConfig.ChainID,
		"other config fields should be preserved")

	// Verify the raw YAML doesn't contain evm_key_name when it's empty
	// (omitempty tag).
	raw, err := os.ReadFile(cfgFile)
	require.NoError(t, err)
	var rawMap map[string]interface{}
	require.NoError(t, yaml.Unmarshal(raw, &rawMap))

	snSection, ok := rawMap["supernode"].(map[string]interface{})
	require.True(t, ok, "supernode section should exist in YAML")
	_, hasEVMKeyName := snSection["evm_key_name"]
	assert.False(t, hasEVMKeyName,
		"evm_key_name should not appear in YAML when empty (omitempty)")
}

// TestDualSigningProtocol verifies the exact signing protocol that the chain
// expects for MsgClaimLegacyAccount:
//   - Legacy: Sign(SHA256(payload)) → chain verifies with VerifySignature(SHA256(payload), sig)
//   - New:    Sign(payload) → chain verifies with VerifySignature(payload, sig)
func TestDualSigningProtocol(t *testing.T) {
	kr := newTestKeyring(t)
	mn := generateMnemonic(t)

	// Create both key types.
	legacyRec, err := kr.NewAccount("legacy", mn, "", "m/44'/118'/0'/0/0", hd.Secp256k1)
	require.NoError(t, err)
	legacyPubKey, err := legacyRec.GetPubKey()
	require.NoError(t, err)
	legacyAddr, err := legacyRec.GetAddress()
	require.NoError(t, err)

	evmRec, err := kr.NewAccount("evm", mn, "", "m/44'/60'/0'/0/0", evmhd.EthSecp256k1)
	require.NoError(t, err)
	evmPubKey, err := evmRec.GetPubKey()
	require.NoError(t, err)
	evmAddr := sdk.AccAddress(evmPubKey.Address())

	payload := []byte("lumera-evm-migration:test-chain-1:76857769:claim:" + legacyAddr.String() + ":" + evmAddr.String())

	// --- Legacy signature protocol ---
	// The supernode passes SHA256(payload) to kr.Sign.
	// secp256k1 internally SHA256s again: Sign(SHA256(SHA256(payload))).
	// The chain verifier does: VerifySignature(SHA256(payload), sig)
	// where VerifySignature internally SHA256s: verify(SHA256(SHA256(payload)), sig).
	hash := sha256.Sum256(payload)
	legacySig, _, err := kr.Sign("legacy", hash[:], signingtypes.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)

	// Simulate chain-side verification: VerifySignature(SHA256(payload), sig).
	assert.True(t, legacyPubKey.VerifySignature(hash[:], legacySig),
		"legacy sig should verify with SHA256(payload) as message")

	// Signing raw payload should NOT verify with SHA256(payload).
	wrongSig, _, err := kr.Sign("legacy", payload, signingtypes.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	assert.False(t, legacyPubKey.VerifySignature(hash[:], wrongSig),
		"signing raw payload should not match SHA256(payload) verification")

	// --- EVM signature protocol ---
	// The supernode passes raw payload to kr.Sign.
	// eth_secp256k1 internally Keccak-256s: Sign(Keccak256(payload)).
	// The chain verifier does: VerifySignature(payload, sig)
	// where VerifySignature internally Keccak-256s: verify(Keccak256(payload), sig).
	evmSig, _, err := kr.Sign("evm", payload, signingtypes.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)

	assert.True(t, evmPubKey.VerifySignature(payload, evmSig),
		"EVM sig should verify with raw payload")
}

// TestMigrationIdempotency verifies that after migration, the legacy key is
// gone and the EVM key under its name passes "not legacy" checks.
func TestMigrationIdempotency(t *testing.T) {
	kr := newTestKeyring(t)
	mn := generateMnemonic(t)

	// Setup: legacy + EVM keys.
	_, err := kr.NewAccount("mykey", mn, "", "m/44'/118'/0'/0/0", hd.Secp256k1)
	require.NoError(t, err)
	_, err = kr.NewAccount("evm-key", mn, "", "m/44'/60'/0'/0/0", evmhd.EthSecp256k1)
	require.NoError(t, err)

	// Verify pre-migration state.
	rec, _ := kr.Key("mykey")
	pub, _ := rec.GetPubKey()
	_, isLegacy := pub.(*secp256k1.PubKey)
	require.True(t, isLegacy, "before migration, mykey should be secp256k1")

	// Simulate migration: delete legacy key, config key_name → "evm-key".
	require.NoError(t, kr.Delete("mykey"))

	// Verify post-migration state: "evm-key" is now the active key.
	rec, err = kr.Key("evm-key")
	require.NoError(t, err)
	pub, err = rec.GetPubKey()
	require.NoError(t, err)

	_, isLegacy = pub.(*secp256k1.PubKey)
	assert.False(t, isLegacy, "evm-key should NOT be secp256k1")

	_, isEVM := pub.(*ethsecp256k1.PubKey)
	assert.True(t, isEVM, "evm-key should be eth_secp256k1")

	// A second migration check with key_name="evm-key" would see no legacy key → no-op.
}
