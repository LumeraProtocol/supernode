package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	evmigrationtypes "github.com/LumeraProtocol/lumera/x/evmigration/types"
	supernodeTypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	snConfig "github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	evmcryptocodec "github.com/cosmos/evm/crypto/codec"
	evmhd "github.com/cosmos/evm/crypto/hd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// newTestKeyring creates an in-memory keyring that supports both legacy secp256k1
// and EVM eth_secp256k1 key algorithms.
func newTestKeyring(t *testing.T) sdkkeyring.Keyring {
	t.Helper()
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	evmcryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	return sdkkeyring.NewInMemory(cdc, evmhd.EthSecp256k1Option())
}

// addLegacyKey creates a secp256k1 key (coin type 118) in the keyring.
func addLegacyKey(t *testing.T, kr sdkkeyring.Keyring, name string) {
	t.Helper()
	_, _, err := kr.NewMnemonic(name, sdkkeyring.English, "m/44'/118'/0'/0/0", "", hd.Secp256k1)
	require.NoError(t, err, "failed to create legacy key %q", name)
}

// addEVMKey creates an eth_secp256k1 key (coin type 60) in the keyring.
func addEVMKey(t *testing.T, kr sdkkeyring.Keyring, name string) {
	t.Helper()
	_, _, err := kr.NewMnemonic(name, sdkkeyring.English, "m/44'/60'/0'/0/0", "", evmhd.EthSecp256k1)
	require.NoError(t, err, "failed to create EVM key %q", name)
}

// evmKeyAddr returns the bech32 address of the EVM key in the keyring.
func evmKeyAddr(t *testing.T, kr sdkkeyring.Keyring, name string) string {
	t.Helper()
	rec, err := kr.Key(name)
	require.NoError(t, err)
	pub, err := rec.GetPubKey()
	require.NoError(t, err)
	return sdk.AccAddress(pub.Address()).String()
}

// --- isLegacyKey / isEthSecp256k1Key tests ---

func TestIsLegacyKey_WithSecp256k1(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "legacy")

	legacy, err := isLegacyKey(kr, "legacy")
	require.NoError(t, err)
	assert.True(t, legacy, "secp256k1 key should be detected as legacy")
}

func TestIsLegacyKey_WithEthSecp256k1(t *testing.T) {
	kr := newTestKeyring(t)
	addEVMKey(t, kr, "evm")

	legacy, err := isLegacyKey(kr, "evm")
	require.NoError(t, err)
	assert.False(t, legacy, "eth_secp256k1 key should not be detected as legacy")
}

func TestIsLegacyKey_KeyNotFound(t *testing.T) {
	kr := newTestKeyring(t)

	_, err := isLegacyKey(kr, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestIsEthSecp256k1Key_WithEVMKey(t *testing.T) {
	kr := newTestKeyring(t)
	addEVMKey(t, kr, "evm")

	isEVM, err := isEthSecp256k1Key(kr, "evm")
	require.NoError(t, err)
	assert.True(t, isEVM)
}

func TestIsEthSecp256k1Key_WithLegacyKey(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "legacy")

	isEVM, err := isEthSecp256k1Key(kr, "legacy")
	require.NoError(t, err)
	assert.False(t, isEVM)
}

func TestValidateLegacyMigrationSetup_NoMigrationNeeded(t *testing.T) {
	kr := newTestKeyring(t)
	addEVMKey(t, kr, "mykey")

	legacy, err := validateLegacyMigrationSetup(kr, "mykey", "")
	require.NoError(t, err)
	assert.False(t, legacy)
}

func TestValidateLegacyMigrationSetup_LegacyKeyWithoutEVMKeyName(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")

	legacy, err := validateLegacyMigrationSetup(kr, "mykey", "")
	require.Error(t, err)
	assert.True(t, legacy)
	assert.Contains(t, err.Error(), "supernode.key_name=\"mykey\"")
	assert.Contains(t, err.Error(), "supernode keys recover <evm-key-name> --mnemonic")
	assert.Contains(t, err.Error(), "evm_key_name: <evm-key-name>")
	assert.Contains(t, err.Error(), "docs/evm-migration.md")
}

func TestValidateLegacyMigrationSetup_EVMKeyWrongType(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addLegacyKey(t, kr, "also-legacy")

	legacy, err := validateLegacyMigrationSetup(kr, "mykey", "also-legacy")
	require.Error(t, err)
	assert.True(t, legacy)
	assert.Contains(t, err.Error(), "not an eth_secp256k1 key")
	assert.Contains(t, err.Error(), "same mnemonic")
}

// --- test doubles ---

// fakeSuperNodeModule is a test stub for the supernode.Module interface.
type fakeSuperNodeModule struct {
	getSupernode func(ctx context.Context, addr string) (*supernodeTypes.SuperNode, error)
}

func (f *fakeSuperNodeModule) GetTopSuperNodesForBlock(ctx context.Context, req *supernodeTypes.QueryGetTopSuperNodesForBlockRequest) (*supernodeTypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return nil, nil
}

func (f *fakeSuperNodeModule) GetSuperNode(ctx context.Context, address string) (*supernodeTypes.QueryGetSuperNodeResponse, error) {
	return nil, nil
}

func (f *fakeSuperNodeModule) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*supernodeTypes.SuperNode, error) {
	if f.getSupernode != nil {
		return f.getSupernode(ctx, address)
	}
	return &supernodeTypes.SuperNode{SupernodeAccount: address}, nil
}

func (f *fakeSuperNodeModule) GetSupernodeWithLatestAddress(ctx context.Context, address string) (*supernode.SuperNodeInfo, error) {
	return nil, nil
}

func (f *fakeSuperNodeModule) GetParams(ctx context.Context) (*supernodeTypes.QueryParamsResponse, error) {
	return nil, nil
}

func (f *fakeSuperNodeModule) ListSuperNodes(ctx context.Context) (*supernodeTypes.QueryListSuperNodesResponse, error) {
	return nil, nil
}

// fakeMigrationClient implements migrationChainClient for testing.
type fakeMigrationClient struct {
	recordResp     *evmigrationtypes.QueryMigrationRecordResponse
	recordErr      error
	estimateResp   *evmigrationtypes.QueryMigrationEstimateResponse
	estimateErr    error
	broadcastErr   error
	broadcastedMsg sdk.Msg // captures the message passed to BroadcastMigrationTx
}

func (f *fakeMigrationClient) MigrationRecord(_ context.Context, _ string) (*evmigrationtypes.QueryMigrationRecordResponse, error) {
	return f.recordResp, f.recordErr
}

func (f *fakeMigrationClient) MigrationEstimate(_ context.Context, _ string) (*evmigrationtypes.QueryMigrationEstimateResponse, error) {
	return f.estimateResp, f.estimateErr
}

func (f *fakeMigrationClient) BroadcastMigrationTx(_ context.Context, msg sdk.Msg) error {
	f.broadcastedMsg = msg
	return f.broadcastErr
}

// newMigrationCfg creates a config with tmpDir for tests that need config persistence.
func newMigrationCfg(t *testing.T, keyName, evmKeyName string) *snConfig.Config {
	t.Helper()
	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = keyName
	cfg.SupernodeConfig.EVMKeyName = evmKeyName
	cfg.BaseDir = t.TempDir()
	return cfg
}

// --- ensureLegacyAccountMigrated: early-return / validation tests ---

func TestEnsureLegacyAccountMigrated_NoMigrationNeeded(t *testing.T) {
	kr := newTestKeyring(t)
	addEVMKey(t, kr, "mykey")

	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = "mykey"

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, nil, nil)
	require.NoError(t, err, "should return nil when key is already EVM")
}

func TestEnsureLegacyAccountMigrated_LegacyKeyNoEVMKeyName(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")

	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = "mykey"

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supernode.key_name=\"mykey\"")
	assert.Contains(t, err.Error(), "same mnemonic")
	assert.Contains(t, err.Error(), "supernode keys recover <evm-key-name> --mnemonic")
	assert.Contains(t, err.Error(), "evm_key_name: <evm-key-name>")
}

func TestEnsureLegacyAccountMigrated_EVMKeyNotFound(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")

	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = "mykey"
	cfg.SupernodeConfig.EVMKeyName = "nonexistent"

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestEnsureLegacyAccountMigrated_EVMKeyWrongType(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addLegacyKey(t, kr, "also-legacy")

	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = "mykey"
	cfg.SupernodeConfig.EVMKeyName = "also-legacy"

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an eth_secp256k1 key")
	assert.Contains(t, err.Error(), "same mnemonic")
}

func TestEnsureLegacyAccountMigrated_AddressCollision(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	legacyRec, err := kr.Key("mykey")
	require.NoError(t, err)
	legacyAddr, err := legacyRec.GetAddress()
	require.NoError(t, err)

	evmRec, err := kr.Key("evm-key")
	require.NoError(t, err)
	evmPubKey, err := evmRec.GetPubKey()
	require.NoError(t, err)

	assert.NotEqual(t, legacyAddr.String(), fmt.Sprintf("%x", evmPubKey.Address()),
		"keys with different coin types should produce different addresses")
}

func TestEnsureLegacyAccountMigrated_Idempotent_AlreadyEVM(t *testing.T) {
	kr := newTestKeyring(t)
	addEVMKey(t, kr, "mykey")

	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = "mykey"
	cfg.SupernodeConfig.EVMKeyName = "some-unused-evm-key"

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, nil, nil)
	require.NoError(t, err)
}

func TestEnsureLegacyAccountMigrated_KeyNameNotFound(t *testing.T) {
	kr := newTestKeyring(t)

	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = "missing"

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- already-migrated (MigrationRecord) tests ---

func TestEnsureLegacyAccountMigrated_AlreadyMigrated_MatchingAddress(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{
			Record: &evmigrationtypes.MigrationRecord{
				NewAddress: newAddr,
			},
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	// Legacy key should be deleted, config updated.
	_, err = kr.Key("mykey")
	assert.Error(t, err, "legacy key should be deleted")
	assert.Equal(t, "evm-key", cfg.SupernodeConfig.KeyName)
	assert.Equal(t, newAddr, cfg.SupernodeConfig.Identity)
	assert.Empty(t, cfg.SupernodeConfig.EVMKeyName)
}

func TestEnsureLegacyAccountMigrated_AlreadyMigrated_MismatchedAddress(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{
			Record: &evmigrationtypes.MigrationRecord{
				NewAddress: "lumera1wrongaddressxyz",
			},
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new address mismatch")
	assert.Contains(t, err.Error(), "lumera1wrongaddressxyz")

	// Legacy key should NOT be deleted on mismatch.
	_, err = kr.Key("mykey")
	assert.NoError(t, err, "legacy key should still exist after mismatch error")
}

func TestEnsureLegacyAccountMigrated_MigrationRecordQueryError_ProceedsToMigrate(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordErr: fmt.Errorf("network timeout"),
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	// Should have broadcast a MsgClaimLegacyAccount (non-validator default).
	require.NotNil(t, mc.broadcastedMsg)
	_, isClaim := mc.broadcastedMsg.(*evmigrationtypes.MsgClaimLegacyAccount)
	assert.True(t, isClaim, "should broadcast MsgClaimLegacyAccount")

	assert.Equal(t, newAddr, cfg.SupernodeConfig.Identity)
}

// --- validator migration tests ---

func TestEnsureLegacyAccountMigrated_ValidatorUsesMsgMigrateValidator(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{}, // no record
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
			IsValidator:  true,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	require.NotNil(t, mc.broadcastedMsg)
	valMsg, isVal := mc.broadcastedMsg.(*evmigrationtypes.MsgMigrateValidator)
	assert.True(t, isVal, "should broadcast MsgMigrateValidator for validator accounts")
	assert.Equal(t, newAddr, valMsg.NewAddress)
}

func TestEnsureLegacyAccountMigrated_NonValidatorUsesMsgClaimLegacyAccount(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
			IsValidator:  false,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	require.NotNil(t, mc.broadcastedMsg)
	_, isClaim := mc.broadcastedMsg.(*evmigrationtypes.MsgClaimLegacyAccount)
	assert.True(t, isClaim, "should broadcast MsgClaimLegacyAccount for non-validator accounts")
}

func TestEnsureLegacyAccountMigrated_EstimateErrorFailsClosed(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp:  &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateErr: fmt.Errorf("estimate query not available"),
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query migration estimate")
	assert.Nil(t, mc.broadcastedMsg, "should not broadcast when migration type cannot be determined")
}

func TestEnsureLegacyAccountMigrated_EstimateWouldNotSucceed(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed:    false,
			RejectionReason: "migration is disabled",
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration is disabled")

	// No broadcast should have occurred.
	assert.Nil(t, mc.broadcastedMsg)
}

// --- multisig legacy account tests ---

func TestEnsureLegacyAccountMigrated_MultisigRefused(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")
	cfg.LumeraClientConfig.ChainID = "lumera-devnet-1"

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{}, // no record
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
			IsValidator:  false,
			IsMultisig:   true,
			Threshold:    2,
			NumSigners:   3,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2-of-3 multisig")
	assert.Contains(t, err.Error(), "automatic migration is not supported")
	assert.Contains(t, err.Error(), "lumerad tx evmigration generate-proof-payload")
	assert.Contains(t, err.Error(), "sign-proof")
	assert.Contains(t, err.Error(), "assemble-proof")
	assert.Contains(t, err.Error(), "submit-proof")
	assert.Contains(t, err.Error(), "lumera-devnet-1")
	assert.Nil(t, mc.broadcastedMsg, "should not broadcast for multisig accounts")
}

// Multisig accounts that were already migrated out-of-band should still reach
// local cleanup via the alreadyMigrated branch — the daemon never looks at the
// proof shape on the chain record, only at the new address.
func TestEnsureLegacyAccountMigrated_MultisigAlreadyMigrated_CleanupProceeds(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{
			Record: &evmigrationtypes.MigrationRecord{
				NewAddress: newAddr,
			},
		},
		// estimateResp intentionally absent — alreadyMigrated short-circuits before the estimate call.
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	// Local cleanup must complete: legacy key deleted, config updated to EVM key.
	_, err = kr.Key("mykey")
	require.Error(t, err, "legacy key should be deleted after offline migration completes")
	assert.Equal(t, "evm-key", cfg.SupernodeConfig.KeyName)
	assert.Equal(t, newAddr, cfg.SupernodeConfig.Identity)
	assert.Nil(t, mc.broadcastedMsg, "no broadcast needed — migration already on-chain")
}

// --- broadcast failure tests ---

func TestEnsureLegacyAccountMigrated_BroadcastFails(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
		broadcastErr: fmt.Errorf("DeliverTx failed: code=7"),
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "legacy account migration failed")
	assert.Contains(t, err.Error(), "DeliverTx failed")

	// Legacy key should NOT be deleted when broadcast fails.
	_, err = kr.Key("mykey")
	assert.NoError(t, err, "legacy key should still exist after broadcast failure")
}

// --- config save failure before key deletion ---

func TestEnsureLegacyAccountMigrated_ConfigSaveFailsBeforeKeyDelete(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")
	// Point BaseDir to a non-writable path to force SaveConfig to fail.
	cfg.BaseDir = "/proc/nonexistent"

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to save updated config")
	assert.Contains(t, err.Error(), newAddr)

	// Legacy key should still exist so the next startup can resume cleanup
	// from the on-chain migration record.
	_, err = kr.Key("mykey")
	assert.NoError(t, err, "legacy key should remain when config save fails")
}

// --- full happy-path end-to-end test ---

func TestEnsureLegacyAccountMigrated_FullHappyPath(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")
	cfg.SupernodeConfig.Identity = "lumera1oldaddr"

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	// Broadcast happened.
	require.NotNil(t, mc.broadcastedMsg)

	// Legacy key deleted.
	_, err = kr.Key("mykey")
	assert.Error(t, err, "legacy key should be deleted")

	// EVM key still accessible.
	_, err = kr.Key("evm-key")
	assert.NoError(t, err, "EVM key should still exist")

	// Config updated.
	assert.Equal(t, "evm-key", cfg.SupernodeConfig.KeyName)
	assert.Equal(t, newAddr, cfg.SupernodeConfig.Identity)
	assert.Empty(t, cfg.SupernodeConfig.EVMKeyName)

	// Config persisted to disk.
	cfgFile := filepath.Join(cfg.BaseDir, DefaultConfigFile)
	loaded, err := snConfig.LoadConfig(cfgFile, cfg.BaseDir)
	require.NoError(t, err)
	assert.Equal(t, "evm-key", loaded.SupernodeConfig.KeyName)
	assert.Equal(t, newAddr, loaded.SupernodeConfig.Identity)
	assert.Empty(t, loaded.SupernodeConfig.EVMKeyName)
}

// --- keyring and config helper tests ---

func TestEnsureLegacyAccountMigrated_ValidationPassesBeforeNetwork(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	legacyRec, err := kr.Key("mykey")
	require.NoError(t, err)
	legacyAddr, err := legacyRec.GetAddress()
	require.NoError(t, err)

	evmRec, err := kr.Key("evm-key")
	require.NoError(t, err)
	evmPubKey, err := evmRec.GetPubKey()
	require.NoError(t, err)
	evmAddr := sdk.AccAddress(evmPubKey.Address())

	assert.NotEqual(t, legacyAddr.String(), evmAddr.String(),
		"legacy and EVM keys should produce different addresses")

	_, errLegacy := kr.Key("mykey")
	assert.NoError(t, errLegacy)
	_, errEVM := kr.Key("evm-key")
	assert.NoError(t, errEVM)
}

func TestKeyDeleteAfterMigration(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "old-key")
	addEVMKey(t, kr, "new-key")

	evmRec, err := kr.Key("new-key")
	require.NoError(t, err)
	evmAddr, err := evmRec.GetAddress()
	require.NoError(t, err)

	require.NoError(t, kr.Delete("old-key"))

	_, err = kr.Key("old-key")
	assert.Error(t, err, "legacy key should be deleted")

	rec, err := kr.Key("new-key")
	require.NoError(t, err)
	addr, err := rec.GetAddress()
	require.NoError(t, err)
	assert.Equal(t, evmAddr.String(), addr.String())

	isEVM, err := isEthSecp256k1Key(kr, "new-key")
	require.NoError(t, err)
	assert.True(t, isEVM, "EVM key should remain eth_secp256k1")
}

func TestConfigUpdateAfterMigration(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &snConfig.Config{
		SupernodeConfig: snConfig.SupernodeConfig{
			KeyName:    "mykey",
			Identity:   "lumera1oldaddr",
			EVMKeyName: "evm-key",
		},
	}
	cfg.BaseDir = tmpDir

	newAddr := "lumera1newaddr"
	cfg.SupernodeConfig.KeyName = "evm-key"
	cfg.SupernodeConfig.Identity = newAddr
	cfg.SupernodeConfig.EVMKeyName = ""

	cfgFile := filepath.Join(tmpDir, DefaultConfigFile)
	err := snConfig.SaveConfig(cfg, cfgFile)
	require.NoError(t, err)

	loaded, err := snConfig.LoadConfig(cfgFile, tmpDir)
	require.NoError(t, err)
	assert.Equal(t, newAddr, loaded.SupernodeConfig.Identity)
	assert.Empty(t, loaded.SupernodeConfig.EVMKeyName)
	assert.Equal(t, "evm-key", loaded.SupernodeConfig.KeyName)
}

func TestConfigSaveCreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "subdir", DefaultConfigFile)

	cfg := &snConfig.Config{}
	cfg.SupernodeConfig.KeyName = "test"
	cfg.SupernodeConfig.Identity = "lumera1test"

	err := snConfig.SaveConfig(cfg, cfgFile)
	require.NoError(t, err)

	_, err = os.Stat(cfgFile)
	require.NoError(t, err, "config file should exist after save")
}

// --- fakeTxServiceClient mocks sdktx.ServiceClient for waitForTxConfirmation tests ---

type fakeTxServiceClient struct {
	getTxResponses []getTxResult // sequential responses; cycles the last one
	getTxCallCount int
}

type getTxResult struct {
	resp *sdktx.GetTxResponse
	err  error
}

func (f *fakeTxServiceClient) GetTx(_ context.Context, _ *sdktx.GetTxRequest, _ ...grpc.CallOption) (*sdktx.GetTxResponse, error) {
	idx := f.getTxCallCount
	if idx >= len(f.getTxResponses) {
		idx = len(f.getTxResponses) - 1
	}
	f.getTxCallCount++
	r := f.getTxResponses[idx]
	return r.resp, r.err
}

// Unused methods — satisfy the interface.
func (f *fakeTxServiceClient) Simulate(_ context.Context, _ *sdktx.SimulateRequest, _ ...grpc.CallOption) (*sdktx.SimulateResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeTxServiceClient) BroadcastTx(_ context.Context, _ *sdktx.BroadcastTxRequest, _ ...grpc.CallOption) (*sdktx.BroadcastTxResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeTxServiceClient) GetTxsEvent(_ context.Context, _ *sdktx.GetTxsEventRequest, _ ...grpc.CallOption) (*sdktx.GetTxsEventResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeTxServiceClient) GetBlockWithTxs(_ context.Context, _ *sdktx.GetBlockWithTxsRequest, _ ...grpc.CallOption) (*sdktx.GetBlockWithTxsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeTxServiceClient) TxDecode(_ context.Context, _ *sdktx.TxDecodeRequest, _ ...grpc.CallOption) (*sdktx.TxDecodeResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeTxServiceClient) TxEncode(_ context.Context, _ *sdktx.TxEncodeRequest, _ ...grpc.CallOption) (*sdktx.TxEncodeResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeTxServiceClient) TxEncodeAmino(_ context.Context, _ *sdktx.TxEncodeAminoRequest, _ ...grpc.CallOption) (*sdktx.TxEncodeAminoResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeTxServiceClient) TxDecodeAmino(_ context.Context, _ *sdktx.TxDecodeAminoRequest, _ ...grpc.CallOption) (*sdktx.TxDecodeAminoResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// --- waitForTxConfirmation tests ---

func TestWaitForTxConfirmation_Success(t *testing.T) {
	mock := &fakeTxServiceClient{
		getTxResponses: []getTxResult{
			{err: fmt.Errorf("tx not found")},             // first poll: not yet indexed
			{resp: &sdktx.GetTxResponse{TxResponse: nil}}, // second poll: no response yet
			{resp: &sdktx.GetTxResponse{ // third poll: confirmed
				TxResponse: &sdk.TxResponse{Code: 0, TxHash: "ABC123"},
			}},
		},
	}

	err := waitForTxConfirmation(context.Background(), mock, "ABC123")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, mock.getTxCallCount, 3, "should have polled at least 3 times")
}

func TestWaitForTxConfirmation_DeliverTxFailure(t *testing.T) {
	mock := &fakeTxServiceClient{
		getTxResponses: []getTxResult{
			{resp: &sdktx.GetTxResponse{
				TxResponse: &sdk.TxResponse{
					Code:      7,
					Codespace: "evmigration",
					RawLog:    "ErrUseValidatorMigration: use MsgMigrateValidator for validator accounts",
				},
			}},
		},
	}

	err := waitForTxConfirmation(context.Background(), mock, "DEADBEEF")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DeliverTx")
	assert.Contains(t, err.Error(), "code=7")
	assert.Contains(t, err.Error(), "ErrUseValidatorMigration")
}

func TestWaitForTxConfirmation_ContextCancelled(t *testing.T) {
	// Mock that never returns success — cancellation should stop it.
	mock := &fakeTxServiceClient{
		getTxResponses: []getTxResult{
			{err: fmt.Errorf("tx not found")},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := waitForTxConfirmation(ctx, mock, "CANCELLED")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled")
}

func TestWaitForTxConfirmation_Timeout(t *testing.T) {
	// Temporarily override the timeout constants for a fast test.
	// We can't change the package-level consts, but we can use a short
	// context deadline that expires before the internal timeout.
	mock := &fakeTxServiceClient{
		getTxResponses: []getTxResult{
			{err: fmt.Errorf("tx not found")},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := waitForTxConfirmation(ctx, mock, "TIMEOUT")
	require.Error(t, err)
	// Either the context deadline or the internal timeout fires.
	assert.True(t,
		contains(err.Error(), "context cancelled") || contains(err.Error(), "timed out"),
		"expected timeout or cancellation error, got: %s", err.Error(),
	)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- validator full happy-path test ---

func TestEnsureLegacyAccountMigrated_ValidatorFullHappyPath(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "val-key")
	addEVMKey(t, kr, "val-evm-key")

	newAddr := evmKeyAddr(t, kr, "val-evm-key")
	cfg := newMigrationCfg(t, "val-key", "val-evm-key")
	cfg.SupernodeConfig.Identity = "lumera1oldvalidator"

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
			IsValidator:  true,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	// Correct message type.
	valMsg, isVal := mc.broadcastedMsg.(*evmigrationtypes.MsgMigrateValidator)
	require.True(t, isVal, "should use MsgMigrateValidator for validator")
	assert.Equal(t, newAddr, valMsg.NewAddress)
	assert.NotEmpty(t, valMsg.NewSignature)
	valSingle := valMsg.LegacyProof.GetSingle()
	require.NotNil(t, valSingle, "expected single-key legacy proof")
	assert.NotEmpty(t, valSingle.Signature)
	assert.NotEmpty(t, valSingle.PubKey)
	assert.Equal(t, evmigrationtypes.SigFormat_SIG_FORMAT_CLI, valSingle.SigFormat)

	// Legacy key deleted.
	_, err = kr.Key("val-key")
	assert.Error(t, err, "legacy key should be deleted")

	// Config updated.
	assert.Equal(t, "val-evm-key", cfg.SupernodeConfig.KeyName)
	assert.Equal(t, newAddr, cfg.SupernodeConfig.Identity)
	assert.Empty(t, cfg.SupernodeConfig.EVMKeyName)

	// Config persisted.
	cfgFile := filepath.Join(cfg.BaseDir, DefaultConfigFile)
	loaded, err := snConfig.LoadConfig(cfgFile, cfg.BaseDir)
	require.NoError(t, err)
	assert.Equal(t, "val-evm-key", loaded.SupernodeConfig.KeyName)
	assert.Equal(t, newAddr, loaded.SupernodeConfig.Identity)
}

// --- supernode verification (non-fatal) tests ---

func TestEnsureLegacyAccountMigrated_SupernodeVerificationFails_NonFatal(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
	}

	snMod := &fakeSuperNodeModule{
		getSupernode: func(_ context.Context, _ string) (*supernodeTypes.SuperNode, error) {
			return nil, fmt.Errorf("supernode not found on-chain")
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, snMod)
	require.NoError(t, err, "supernode verification failure should be non-fatal")

	// Migration still completed.
	assert.Equal(t, newAddr, cfg.SupernodeConfig.Identity)
	_, err = kr.Key("mykey")
	assert.Error(t, err, "legacy key should still be deleted")
}

func TestEnsureLegacyAccountMigrated_SupernodeVerificationReturnsNil_NonFatal(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
	}

	snMod := &fakeSuperNodeModule{
		getSupernode: func(_ context.Context, _ string) (*supernodeTypes.SuperNode, error) {
			return nil, nil // not found, no error
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, snMod)
	require.NoError(t, err, "nil supernode result should be non-fatal")
}

// --- already-migrated: config persistence verified ---

func TestEnsureLegacyAccountMigrated_AlreadyMigrated_ConfigPersisted(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	newAddr := evmKeyAddr(t, kr, "evm-key")
	cfg := newMigrationCfg(t, "mykey", "evm-key")
	cfg.SupernodeConfig.Identity = "lumera1oldaddr"

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{
			Record: &evmigrationtypes.MigrationRecord{
				NewAddress: newAddr,
			},
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	// No broadcast should have occurred.
	assert.Nil(t, mc.broadcastedMsg, "already-migrated should skip broadcast")

	// Config file should be saved to disk.
	cfgFile := filepath.Join(cfg.BaseDir, DefaultConfigFile)
	loaded, err := snConfig.LoadConfig(cfgFile, cfg.BaseDir)
	require.NoError(t, err)
	assert.Equal(t, "evm-key", loaded.SupernodeConfig.KeyName)
	assert.Equal(t, newAddr, loaded.SupernodeConfig.Identity)
	assert.Empty(t, loaded.SupernodeConfig.EVMKeyName)
}

// --- broadcast failure preserves full state ---

func TestEnsureLegacyAccountMigrated_BroadcastFails_PreservesFullState(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")
	cfg.SupernodeConfig.Identity = "lumera1original"

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
		broadcastErr: fmt.Errorf("tx failed in block execution (DeliverTx): code=7 codespace=evmigration"),
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DeliverTx")

	// Everything should be preserved — no local state mutation on broadcast failure.
	_, err = kr.Key("mykey")
	assert.NoError(t, err, "legacy key should NOT be deleted")
	_, err = kr.Key("evm-key")
	assert.NoError(t, err, "EVM key should still exist")
	assert.Equal(t, "mykey", cfg.SupernodeConfig.KeyName, "key_name should be unchanged")
	assert.Equal(t, "lumera1original", cfg.SupernodeConfig.Identity, "identity should be unchanged")
	assert.Equal(t, "evm-key", cfg.SupernodeConfig.EVMKeyName, "evm_key_name should be unchanged")
}

// --- migration record nil record field (empty response, not nil) ---

func TestEnsureLegacyAccountMigrated_MigrationRecordNilRecord_ProceedsToMigrate(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{
			Record: nil, // response exists but record is nil
		},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
		},
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)
	assert.NotNil(t, mc.broadcastedMsg, "should broadcast when Record is nil")
}

// --- dual signing: verify the message contains valid signatures and correct addresses ---

func TestEnsureLegacyAccountMigrated_MessageFieldsCorrect(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	legacyRec, err := kr.Key("mykey")
	require.NoError(t, err)
	legacyAddr, err := legacyRec.GetAddress()
	require.NoError(t, err)
	legacyPub, err := legacyRec.GetPubKey()
	require.NoError(t, err)

	evmRec, err := kr.Key("evm-key")
	require.NoError(t, err)
	evmPub, err := evmRec.GetPubKey()
	require.NoError(t, err)
	newAddr := sdk.AccAddress(evmPub.Address()).String()

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordResp: &evmigrationtypes.QueryMigrationRecordResponse{},
		estimateResp: &evmigrationtypes.QueryMigrationEstimateResponse{
			WouldSucceed: true,
			IsValidator:  false,
		},
	}

	err = ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.NoError(t, err)

	claimMsg, ok := mc.broadcastedMsg.(*evmigrationtypes.MsgClaimLegacyAccount)
	require.True(t, ok)

	assert.Equal(t, legacyAddr.String(), claimMsg.LegacyAddress)
	assert.Equal(t, newAddr, claimMsg.NewAddress)
	assert.NotEmpty(t, claimMsg.NewSignature)
	claimSingle := claimMsg.LegacyProof.GetSingle()
	require.NotNil(t, claimSingle, "expected single-key legacy proof")
	assert.Equal(t, legacyPub.Bytes(), claimSingle.PubKey)
	assert.NotEmpty(t, claimSingle.Signature)
	assert.Equal(t, evmigrationtypes.SigFormat_SIG_FORMAT_CLI, claimSingle.SigFormat)
}

// --- estimate with both record query error and estimate error ---

func TestEnsureLegacyAccountMigrated_BothQueriesFail_FailsClosed(t *testing.T) {
	kr := newTestKeyring(t)
	addLegacyKey(t, kr, "mykey")
	addEVMKey(t, kr, "evm-key")

	cfg := newMigrationCfg(t, "mykey", "evm-key")

	mc := &fakeMigrationClient{
		recordErr:   fmt.Errorf("record query unreachable"),
		estimateErr: fmt.Errorf("estimate query unreachable"),
	}

	err := ensureLegacyAccountMigrated(context.Background(), kr, cfg, mc, &fakeSuperNodeModule{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query migration estimate")
	assert.Nil(t, mc.broadcastedMsg, "should not broadcast when both migration queries are unavailable")
}
