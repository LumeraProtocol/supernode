package cmd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	upgradetypes "cosmossdk.io/x/upgrade/types"
	lcfg "github.com/LumeraProtocol/lumera/config"
	evmigrationtypes "github.com/LumeraProtocol/lumera/x/evmigration/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	lumeracodec "github.com/LumeraProtocol/supernode/v2/pkg/lumera/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	snConfig "github.com/LumeraProtocol/supernode/v2/supernode/config"
	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	"google.golang.org/grpc"
)

const (
	// evmModuleName is the name of the EVM module in the chain's module version map.
	evmModuleName = "evm"
)

func legacyKeyMigrationInstructions(keyName string) string {
	return fmt.Sprintf(
		"Lumera is running with the EVM module enabled, but supernode.key_name=%q still points to a legacy secp256k1 key (coin type 118).\n\n"+
			"To continue:\n"+
			"  1. Derive a new EVM key from the same mnemonic:\n"+
			"     supernode keys recover <evm-key-name> --mnemonic \"your twelve or twenty four words ...\"\n"+
			"  2. Add the new key name to config.yml under the supernode section:\n"+
			"     key_name: %s\n"+
			"     evm_key_name: <evm-key-name>\n"+
			"  3. Restart supernode so the automatic migration can move the on-chain account to the new EVM address.\n\n"+
			"See docs/evm-migration.md for the full migration procedure.",
		keyName,
		keyName,
	)
}

func validateLegacyMigrationSetup(kr cKeyring.Keyring, keyName, evmKeyName string) (bool, error) {
	legacy, err := isLegacyKey(kr, keyName)
	if err != nil {
		return false, err
	}
	if !legacy {
		return false, nil
	}

	evmKeyName = strings.TrimSpace(evmKeyName)
	if evmKeyName == "" {
		return true, fmt.Errorf("%s", legacyKeyMigrationInstructions(keyName))
	}

	isEVM, err := isEthSecp256k1Key(kr, evmKeyName)
	if err != nil {
		return true, fmt.Errorf("evm_key_name %q: %w\n\n%s", evmKeyName, err, legacyKeyMigrationInstructions(keyName))
	}
	if !isEVM {
		return true, fmt.Errorf(
			"evm_key_name %q is not an eth_secp256k1 key.\n\n%s",
			evmKeyName,
			legacyKeyMigrationInstructions(keyName),
		)
	}

	return true, nil
}

// requireEVMChain verifies that the connected Lumera chain has the EVM module
// active. If the module is absent, this supernode binary is incompatible and
// must not proceed.
func requireEVMChain(ctx context.Context, conn *grpc.ClientConn) error {
	client := upgradetypes.NewQueryClient(conn)
	resp, err := client.ModuleVersions(ctx, &upgradetypes.QueryModuleVersionsRequest{
		ModuleName: evmModuleName,
	})
	if err != nil {
		return fmt.Errorf("failed to query chain module versions: %w", err)
	}
	if len(resp.ModuleVersions) == 0 {
		return fmt.Errorf(
			"connected Lumera chain does not have EVM support (module %q not found). "+
				"This supernode binary requires an EVM-enabled Lumera chain. "+
				"Please upgrade your Lumera node or connect to an EVM-enabled chain",
			evmModuleName,
		)
	}

	logtrace.Info(ctx, "EVM module detected on chain", logtrace.Fields{
		"module":  evmModuleName,
		"version": resp.ModuleVersions[0].Version,
	})
	return nil
}

// isLegacyKey returns true if the key stored in the keyring under keyName uses
// the pre-EVM secp256k1 algorithm (coin type 118) rather than the EVM-compatible
// eth_secp256k1 (coin type 60).
func isLegacyKey(kr cKeyring.Keyring, keyName string) (bool, error) {
	rec, err := kr.Key(keyName)
	if err != nil {
		return false, fmt.Errorf("key %q not found in keyring: %w", keyName, err)
	}
	pubKey, err := rec.GetPubKey()
	if err != nil {
		return false, fmt.Errorf("failed to get public key for %q: %w", keyName, err)
	}
	_, isSecp := pubKey.(*secp256k1.PubKey)
	return isSecp, nil
}

// isEthSecp256k1Key returns true if the key stored in the keyring under keyName
// uses the EVM-compatible eth_secp256k1 algorithm.
func isEthSecp256k1Key(kr cKeyring.Keyring, keyName string) (bool, error) {
	legacy, err := isLegacyKey(kr, keyName)
	if err != nil {
		return false, err
	}
	return !legacy, nil
}

// migrationChainClient abstracts chain interactions needed by the migration
// flow, enabling unit tests without a live gRPC connection.
type migrationChainClient interface {
	MigrationRecord(ctx context.Context, legacyAddr string) (*evmigrationtypes.QueryMigrationRecordResponse, error)
	MigrationEstimate(ctx context.Context, legacyAddr string) (*evmigrationtypes.QueryMigrationEstimateResponse, error)
	BroadcastMigrationTx(ctx context.Context, msg sdk.Msg) error
}

// grpcMigrationClient implements migrationChainClient using a real gRPC connection.
type grpcMigrationClient struct {
	conn *grpc.ClientConn
}

func (g *grpcMigrationClient) MigrationRecord(ctx context.Context, legacyAddr string) (*evmigrationtypes.QueryMigrationRecordResponse, error) {
	return evmigrationtypes.NewQueryClient(g.conn).MigrationRecord(ctx, &evmigrationtypes.QueryMigrationRecordRequest{
		LegacyAddress: legacyAddr,
	})
}

func (g *grpcMigrationClient) MigrationEstimate(ctx context.Context, legacyAddr string) (*evmigrationtypes.QueryMigrationEstimateResponse, error) {
	return evmigrationtypes.NewQueryClient(g.conn).MigrationEstimate(ctx, &evmigrationtypes.QueryMigrationEstimateRequest{
		LegacyAddress: legacyAddr,
	})
}

func (g *grpcMigrationClient) BroadcastMigrationTx(ctx context.Context, msg sdk.Msg) error {
	return broadcastMigrationTx(ctx, g.conn, msg)
}

// ensureLegacyAccountMigrated detects whether the keyring contains a legacy
// secp256k1 key under keyName. If so, it uses the new EVM key (evmKeyName)
// already imported in the keyring to perform dual-signed migration.
//
// The function is designed to be rerunnable: if a previous run partially
// completed (e.g. tx was broadcast but keyring cleanup failed), re-running
// will detect the state and resume from where it left off.
//
// Steps:
//  1. Validates evmKeyName exists and is eth_secp256k1
//  2. Checks if the account was already migrated on-chain (MigrationRecord query)
//  3. Runs MigrationEstimate to pre-check whether migration would succeed
//  4. Signs legacy proof with the legacy key, new proof with the EVM key
//  5. Broadcasts MsgClaimLegacyAccount or MsgMigrateValidator
//  6. Verifies the new address is registered as supernode on-chain
//  7. Updates config (identity → new address, clears evm_key_name) and saves
//  8. Deletes the old legacy key (EVM key stays under its name; config key_name updated)
func ensureLegacyAccountMigrated(
	ctx context.Context,
	kr cKeyring.Keyring,
	cfg *snConfig.Config,
	chainClient migrationChainClient,
	snModule supernode.Module,
) error {
	keyName := cfg.SupernodeConfig.KeyName
	evmKeyName := cfg.SupernodeConfig.EVMKeyName

	legacy, err := validateLegacyMigrationSetup(kr, keyName, evmKeyName)
	if err != nil {
		return err
	}
	if !legacy {
		logtrace.Debug(ctx, "Key uses eth_secp256k1, no migration needed", logtrace.Fields{"key": keyName})
		return nil
	}

	// Get legacy key info.
	legacyRec, err := kr.Key(keyName)
	if err != nil {
		return fmt.Errorf("failed to read legacy key %q: %w", keyName, err)
	}
	legacyPubKey, err := legacyRec.GetPubKey()
	if err != nil {
		return fmt.Errorf("failed to get public key for %q: %w", keyName, err)
	}
	legacyAddr, err := legacyRec.GetAddress()
	if err != nil {
		return fmt.Errorf("failed to get address from key %q: %w", keyName, err)
	}

	// Get new EVM key info.
	newRec, err := kr.Key(evmKeyName)
	if err != nil {
		return fmt.Errorf("failed to read EVM key %q: %w", evmKeyName, err)
	}
	newPubKey, err := newRec.GetPubKey()
	if err != nil {
		return fmt.Errorf("failed to get public key for %q: %w", evmKeyName, err)
	}
	newAddr := sdk.AccAddress(newPubKey.Address())

	if legacyAddr.Equals(newAddr) {
		return fmt.Errorf(
			"legacy address equals new address %s — this should not happen with different coin types",
			legacyAddr.String(),
		)
	}

	logtrace.Warn(ctx, "Legacy secp256k1 key detected — EVM account migration required", logtrace.Fields{
		"key":            keyName,
		"evm_key":        evmKeyName,
		"legacy_address": legacyAddr.String(),
		"new_address":    newAddr.String(),
	})

	// Check if the account was already migrated on-chain (handles rerun after
	// a previous broadcast succeeded but local cleanup failed).
	alreadyMigrated := false

	recordResp, err := chainClient.MigrationRecord(ctx, legacyAddr.String())
	if err != nil {
		logtrace.Warn(ctx, "Could not query migration record (will attempt migration anyway)", logtrace.Fields{
			"legacy_address": legacyAddr.String(),
			"error":          err.Error(),
		})
	} else if recordResp.Record != nil {
		// Verify on-chain new address matches our locally configured EVM key.
		if recordResp.Record.NewAddress != newAddr.String() {
			return fmt.Errorf(
				"migration record exists on-chain but new address mismatch: "+
					"on-chain=%s, local evm_key=%s (address=%s). "+
					"Check that evm_key_name in config matches the key used for the original migration",
				recordResp.Record.NewAddress, evmKeyName, newAddr.String(),
			)
		}
		alreadyMigrated = true
		logtrace.Info(ctx, "Account already migrated on-chain, skipping broadcast", logtrace.Fields{
			"legacy_address": legacyAddr.String(),
			"new_address":    recordResp.Record.NewAddress,
		})
	}

	if !alreadyMigrated {
		// Pre-flight: run MigrationEstimate to catch issues before broadcasting.
		isValidator := false
		estimateResp, err := chainClient.MigrationEstimate(ctx, legacyAddr.String())
		if err != nil {
			return fmt.Errorf(
				"failed to query migration estimate for %s: %w\n"+
					"Unable to determine whether this account requires MsgClaimLegacyAccount or MsgMigrateValidator. "+
					"Safe to retry — just restart the supernode.",
				legacyAddr.String(), err,
			)
		} else {
			isValidator = estimateResp.IsValidator
			logtrace.Info(ctx, "Migration estimate", logtrace.Fields{
				"legacy_address":   legacyAddr.String(),
				"would_succeed":    estimateResp.WouldSucceed,
				"rejection_reason": estimateResp.RejectionReason,
				"is_validator":     estimateResp.IsValidator,
				"total_touched":    estimateResp.TotalTouched,
			})
			if !estimateResp.WouldSucceed {
				return fmt.Errorf(
					"migration estimate indicates migration would fail for %s: %s",
					legacyAddr.String(), estimateResp.RejectionReason,
				)
			}
		}

		// Choose the correct payload kind based on whether the legacy account
		// is a validator operator. The chain rejects MsgClaimLegacyAccount for
		// validators and requires MsgMigrateValidator instead.
		payloadKind := "claim"
		if isValidator {
			payloadKind = "validator"
		}

		// Build the canonical migration payload.
		// Format: lumera-evm-migration:<chainID>:<evmChainID>:<kind>:<legacyAddr>:<newAddr>
		chainID := cfg.LumeraClientConfig.ChainID
		payload := []byte(fmt.Sprintf("lumera-evm-migration:%s:%d:%s:%s:%s", chainID, lcfg.EVMChainID, payloadKind, legacyAddr.String(), newAddr.String()))

		// Sign with legacy key from keyring.
		// secp256k1.Sign(msg) internally computes SHA256(msg), and the chain verifier
		// does SHA256(payload) then VerifySignature(hash, sig) which also does SHA256.
		// So we pass SHA256(payload) to the keyring, giving us Sign(SHA256(payload))
		// which produces a signature over SHA256(SHA256(payload)) — matching the verifier.
		hash := sha256.Sum256(payload)
		legacySig, _, err := kr.Sign(keyName, hash[:], signingtypes.SignMode_SIGN_MODE_DIRECT)
		if err != nil {
			return fmt.Errorf("failed to sign migration payload with legacy key: %w", err)
		}

		// Sign with new EVM key from keyring (eth_secp256k1 signs raw payload;
		// internally uses Keccak-256).
		newSig, _, err := kr.Sign(evmKeyName, payload, signingtypes.SignMode_SIGN_MODE_DIRECT)
		if err != nil {
			return fmt.Errorf("failed to sign migration payload with EVM key: %w", err)
		}

		// Build the appropriate message type.
		var msg sdk.Msg
		if isValidator {
			msg = &evmigrationtypes.MsgMigrateValidator{
				NewAddress:      newAddr.String(),
				LegacyAddress:   legacyAddr.String(),
				LegacyPubKey:    legacyPubKey.Bytes(),
				LegacySignature: legacySig,
				NewSignature:    newSig,
			}
			logtrace.Info(ctx, "Validator account detected — using MsgMigrateValidator", logtrace.Fields{
				"legacy_address": legacyAddr.String(),
				"new_address":    newAddr.String(),
			})
		} else {
			msg = &evmigrationtypes.MsgClaimLegacyAccount{
				NewAddress:      newAddr.String(),
				LegacyAddress:   legacyAddr.String(),
				LegacyPubKey:    legacyPubKey.Bytes(),
				LegacySignature: legacySig,
				NewSignature:    newSig,
			}
		}

		if err := msg.(interface{ ValidateBasic() error }).ValidateBasic(); err != nil {
			return fmt.Errorf("migration message validation failed: %w", err)
		}

		if err := chainClient.BroadcastMigrationTx(ctx, msg); err != nil {
			return fmt.Errorf(
				"legacy account migration failed: %w\n\n"+
					"Common causes:\n"+
					"  - Migration is disabled on-chain\n"+
					"  - Legacy account does not exist on-chain\n"+
					"  - Wrong mnemonic used to derive the EVM key\n\n"+
					"This operation is safe to retry — just restart the supernode.",
				err,
			)
		}

		logtrace.Info(ctx, "Legacy account migration tx broadcast successfully", logtrace.Fields{
			"legacy_address": legacyAddr.String(),
			"new_address":    newAddr.String(),
			"is_validator":   isValidator,
		})
	}

	// Verify the new address is registered as a supernode on-chain.
	sn, err := snModule.GetSupernodeBySupernodeAddress(ctx, newAddr.String())
	if err != nil {
		logtrace.Warn(ctx, "Could not verify supernode registration for new address (non-fatal)", logtrace.Fields{
			"new_address": newAddr.String(),
			"error":       err.Error(),
		})
	} else if sn != nil {
		logtrace.Info(ctx, "New address confirmed as registered supernode", logtrace.Fields{
			"new_address":       newAddr.String(),
			"supernode_account": sn.SupernodeAccount,
		})
	}

	// Update config: key_name → evm key, identity → new address, clear evm_key_name.
	cfg.SupernodeConfig.KeyName = evmKeyName
	cfg.SupernodeConfig.Identity = newAddr.String()
	cfg.SupernodeConfig.EVMKeyName = ""

	cfgFile := filepath.Join(cfg.BaseDir, DefaultConfigFile)
	if err := snConfig.SaveConfig(cfg, cfgFile); err != nil {
		return fmt.Errorf(
			"migration complete but failed to save updated config: %w\n"+
				"Please manually update config.yaml:\n"+
				"  - Set key_name to %s\n"+
				"  - Set identity to %s\n"+
				"  - Remove evm_key_name",
			err, evmKeyName, newAddr.String(),
		)
	}

	// Delete the old legacy key only after the updated config is safely on disk.
	// If config persistence fails, keeping the legacy key allows the next startup
	// to resume from the on-chain migration record and complete local cleanup.
	if err := kr.Delete(keyName); err != nil {
		return fmt.Errorf(
			"migration complete and config updated, but failed to delete legacy key %q: %w\n"+
				"Please manually remove the old key.\n"+
				"Safe to retry — just restart the supernode.",
			keyName, err,
		)
	}

	logtrace.Info(ctx, "EVM migration complete — legacy key removed, config updated", logtrace.Fields{
		"key_name":       evmKeyName,
		"legacy_address": legacyAddr.String(),
		"new_address":    newAddr.String(),
	})
	return nil
}

const (
	// txConfirmPollInterval is how often we poll for tx inclusion.
	txConfirmPollInterval = 2 * time.Second
	// txConfirmTimeout is the maximum time to wait for tx inclusion in a block.
	txConfirmTimeout = 60 * time.Second
)

// broadcastMigrationTx builds an unsigned Cosmos tx containing the migration
// message, simulates gas, broadcasts it via SYNC mode, and then polls until
// the tx is confirmed in a block (DeliverTx). This ensures we only mutate
// local state (keyring, config) after the chain has committed the migration.
func broadcastMigrationTx(ctx context.Context, conn *grpc.ClientConn, msg sdk.Msg) error {
	encCfg := lumeracodec.GetEncodingConfig()

	txBuilder := encCfg.TxConfig.NewTxBuilder()
	if err := txBuilder.SetMsgs(msg); err != nil {
		return fmt.Errorf("failed to set message on tx builder: %w", err)
	}

	// Simulate to get gas estimate. Migration txs are fee-exempt on chain, but
	// we still need a valid gas limit.
	txBytes, err := encCfg.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return fmt.Errorf("failed to encode tx for simulation: %w", err)
	}

	txClient := sdktx.NewServiceClient(conn)
	simRes, err := txClient.Simulate(ctx, &sdktx.SimulateRequest{TxBytes: txBytes})
	if err != nil {
		return fmt.Errorf("simulation failed: %w", err)
	}

	// Apply gas adjustment.
	gasLimit := uint64(float64(simRes.GasInfo.GasUsed) * 1.5)
	txBuilder.SetGasLimit(gasLimit)

	// Re-encode with gas limit set.
	txBytes, err = encCfg.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return fmt.Errorf("failed to encode tx for broadcast: %w", err)
	}

	// Broadcast with SYNC mode (waits for CheckTx only).
	resp, err := txClient.BroadcastTx(ctx, &sdktx.BroadcastTxRequest{
		TxBytes: txBytes,
		Mode:    sdktx.BroadcastMode_BROADCAST_MODE_SYNC,
	})
	if err != nil {
		return fmt.Errorf("broadcast error: %w", err)
	}
	if resp.TxResponse != nil && resp.TxResponse.Code != 0 {
		return fmt.Errorf(
			"tx rejected at CheckTx: code=%d codespace=%s raw_log=%s",
			resp.TxResponse.Code,
			resp.TxResponse.Codespace,
			resp.TxResponse.RawLog,
		)
	}

	txHash := resp.TxResponse.TxHash
	logtrace.Info(ctx, "Migration tx passed CheckTx, waiting for block confirmation", logtrace.Fields{
		"tx_hash": txHash,
	})

	// Poll for tx inclusion in a block (DeliverTx confirmation).
	if err := waitForTxConfirmation(ctx, txClient, txHash); err != nil {
		return fmt.Errorf("migration tx %s was not confirmed: %w", txHash, err)
	}

	logtrace.Info(ctx, "Migration tx confirmed in block", logtrace.Fields{
		"tx_hash": txHash,
	})
	return nil
}

// waitForTxConfirmation polls GetTx until the transaction is included in a
// block or the timeout is reached.
func waitForTxConfirmation(ctx context.Context, txClient sdktx.ServiceClient, txHash string) error {
	deadline := time.After(txConfirmTimeout)
	ticker := time.NewTicker(txConfirmPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for tx confirmation: %w", ctx.Err())
		case <-deadline:
			return fmt.Errorf("timed out after %s waiting for tx %s to be included in a block", txConfirmTimeout, txHash)
		case <-ticker.C:
			txResp, err := txClient.GetTx(ctx, &sdktx.GetTxRequest{Hash: txHash})
			if err != nil {
				// Tx not yet indexed — keep polling.
				logtrace.Debug(ctx, "Tx not yet found, polling...", logtrace.Fields{
					"tx_hash": txHash,
					"error":   err.Error(),
				})
				continue
			}
			if txResp.TxResponse == nil {
				continue
			}
			// Tx is in a block — check DeliverTx result.
			if txResp.TxResponse.Code != 0 {
				return fmt.Errorf(
					"tx failed in block execution (DeliverTx): code=%d codespace=%s raw_log=%s",
					txResp.TxResponse.Code,
					txResp.TxResponse.Codespace,
					txResp.TxResponse.RawLog,
				)
			}
			return nil
		}
	}
}
