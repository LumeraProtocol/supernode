# EVM Account Migration

This document describes the EVM migration support added to the Lumera supernode.
It covers how operators migrate from legacy secp256k1 keys (coin type 118) to
EVM-compatible eth_secp256k1 keys (coin type 60), the architecture of the
migration flow, and all related code changes.

## Overview

The Lumera chain now requires EVM-compatible keys (`eth_secp256k1`, BIP44 coin
type 60). Existing supernodes that were set up with legacy Cosmos keys
(`secp256k1`, coin type 118) must migrate their on-chain state — balances,
delegations, supernode registration, and optionally validator state — to a new
address derived from the same mnemonic under the EVM HD path.

The migration is:

- **One-time**: runs automatically at superno0de startup when a legacy key is detected
- **Rerunnable**: safe to retry if interrupted at any point
- **Self-authenticating**: uses dual signatures (legacy + new key) embedded in the
  message, so no Cosmos-level tx signing is needed

## Operator Guide

### Prerequisites

1. Your supernode binary must be the EVM-compatible version.
2. The connected Lumera chain must have the`evm` module active.
3. You need the**mnemonic** used to create your original supernode key.

### Migration Steps

1. **Derive your new EVM key** from the same mnemonic:

   ```bash
   supernode keys recover --name evm-key --mnemonic "your twelve or twenty four words ..."
   ```

   This creates an `eth_secp256k1` key under the name `evm-key` using HD path
   `m/44'/60'/0'/0/0`. The resulting address will be different from your legacy
   address — this is expected.
2. **Add `evm_key_name` to your config.yaml** under the `supernode` section:

   ```yaml
   supernode:
     key_name: mykey           # your existing legacy key name
     evm_key_name: evm-key     # the name you used in step 1
     identity: lumera1...      # your current legacy address
   ```
3. **Restart the supernode**. On startup it will:

   - Detect the legacy`secp256k1` key under`key_name`
   - Validate that`evm_key_name` points to a valid`eth_secp256k1` key
   - Query the chain for an existing migration record (handles reruns)
   - Run a pre-flight`MigrationEstimate` to check if the migration would succeed
   - Sign the migration payload with both keys
   - Broadcast`MsgClaimLegacyAccount` (or`MsgMigrateValidator` for validators)
   - Wait for block confirmation (DeliverTx)
   - Delete the legacy key from the keyring
   - Update`config.yaml`:`key_name` ->`evm-key`,`identity` -> new address,`evm_key_name` cleared
4. **After migration completes**, your config will look like:

   ```yaml
   supernode:
     key_name: evm-key
     identity: lumera1<new-evm-address>
   ```

   The `evm_key_name` field is automatically removed. No further action is needed.

### Troubleshooting

| Error                                                 | Cause                                                                                 | Fix                                                                                       |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `no evm_key_name configured`                        | Legacy key detected but config missing `evm_key_name`                               | Add `evm_key_name` to config and restart                                                |
| `not an eth_secp256k1 key`                          | `evm_key_name` points to a secp256k1 key (wrong derivation)                         | Re-derive using `supernode keys recover` (uses coin type 60)                            |
| `new address mismatch`                              | On-chain migration record has a different destination address than your local EVM key | Your `evm_key_name` doesn't match the key originally used for migration; fix the config |
| `migration estimate indicates migration would fail` | Chain rejected the pre-flight check                                                   | Check `rejection_reason` in logs; migration may be disabled or account may not exist    |
| `migration tx was not confirmed`                    | Tx was not included in a block within 60s                                             | Restart to retry; the check is idempotent                                                 |
| `failed to save updated config`                     | Config file write error after successful migration                                    | Manually update config as instructed in the error message                                 |

## Chain-Side Reference

The on-chain `x/evmigration` module is documented in detail in the Lumera chain
repo at `docs/evm-integration.md` (section "Legacy Account Migration"). Key
aspects relevant to supernode operators:

### What the chain does when it processes the migration message

**`MsgClaimLegacyAccount` (non-validator accounts):**

1. Pre-checks: params enabled, migration window open, rate limit, dual-signature verification
2. Withdraw pending distribution rewards to legacy bank balance
3. Re-key staking (delegations, unbonding, redelegations)
4. Migrate auth account record (vesting-aware)
5. Transfer all bank balances from legacy to new address
6. Finalize vesting at new address (if applicable)
7. Re-key authz grants and feegrant allowances
8. Update supernode account field
9. Update action creator/supernode references
10. Update claim destAddress references
11. Store MigrationRecord, increment counters, emit event

**`MsgMigrateValidator` (validator operator accounts):**

Same as above, plus validator record re-keying (operator address, consensus key
mapping, power index updates). The chain rejects `MsgClaimLegacyAccount` for
validator operators with `ErrUseValidatorMigration`.

### Migration parameters (chain-side)

| Param                         | Default  | Description                               |
| ----------------------------- | -------- | ----------------------------------------- |
| `enable_migration`          | `true` | Master switch                             |
| `migration_end_time`        | `0`    | Unix timestamp deadline (0 = no deadline) |
| `max_migrations_per_block`  | `50`   | Rate limit                                |
| `max_validator_delegations` | `2000` | Max delegators for validator migration    |

### Fee waiving

Migration transactions are fee-exempt on-chain (the new address has zero balance
before migration). The `ante/evmigration_fee_decorator.go` decorator handles this.

## Architecture

### Migration Flow

```
Startup
  |
  v
requireEVMChain()
  |  Queries upgrade module for "evm" module version.
  |  Fails fast if chain doesn't have EVM support.
  v
ensureLegacyAccountMigrated()
  |
  v
isLegacyKey(key_name)?
  |-- No  --> return nil (already EVM, no migration needed)
  |-- Yes --> continue
  v
Validate evm_key_name exists and is eth_secp256k1
  v
Query MigrationRecord(legacy_address)
  |-- Record exists, NewAddress matches --> skip broadcast (already migrated)
  |-- Record exists, NewAddress differs  --> ERROR (config mismatch)
  |-- Query error                        --> proceed anyway (best-effort)
  |-- No record                          --> continue to broadcast
  v
Query MigrationEstimate(legacy_address)
  |-- WouldSucceed=false --> ERROR (with rejection reason)
  |-- Query error        --> proceed anyway, default isValidator=false
  |-- WouldSucceed=true  --> capture IsValidator flag
  v
Build payload: "lumera-evm-migration:{claim|validator}:<legacy>:<new>"
  v
Dual sign:
  - Legacy: kr.Sign(SHA256(payload))  -- secp256k1 internally SHA256s again
  - EVM:    kr.Sign(payload)          -- eth_secp256k1 internally Keccak-256s
  v
Build message:
  - IsValidator=true  --> MsgMigrateValidator
  - IsValidator=false --> MsgClaimLegacyAccount
  v
broadcastMigrationTx()
  |  Simulate gas -> broadcast SYNC -> poll GetTx for block confirmation
  v
Verify new address registered as supernode (non-fatal)
  v
Delete legacy key from keyring
  v
Update config (key_name, identity, evm_key_name) and save
```

### Signing Protocol

The chain expects different signing protocols for each key type:

**Legacy (secp256k1):**

```
supernode:  hash = SHA256(payload)
            sig  = kr.Sign(hash)        -- internally: Sign(SHA256(hash))
chain:      VerifySignature(hash, sig)   -- internally: verify(SHA256(hash), sig)
```

**EVM (eth_secp256k1):**

```
supernode:  sig = kr.Sign(payload)       -- internally: Sign(Keccak256(payload))
chain:      VerifySignature(payload, sig) -- internally: verify(Keccak256(payload), sig)
```

### P2P Bootstrap Refresh After Migration

When an EVM migration occurs, all supernodes change their on-chain addresses
simultaneously. Without intervention, the P2P layer would keep stale addresses
in its routing table for up to 10 minutes (the normal bootstrap refresh
interval), causing handshake failures.

Three mechanisms work together to resolve this:

1. **Immediate bootstrap refresh**: After migration completes and the Lumera
   client is reloaded, `start.go` calls `p2pService.NotifyEVMMigration()`.
   This triggers an immediate `SyncBootstrapOnce()` which re-queries
   `ListSuperNodes()` from the chain and updates the routing table with
   current addresses.

2. **Accelerated refresh window**: After the migration signal, the bootstrap
   refresher switches from the normal 10-minute interval to a **1-minute
   interval for 5 cycles**. This catches peers that migrate slightly later
   (staggered startup, network delays). After 5 accelerated cycles it
   automatically reverts to the normal 10-minute cadence.

3. **Staggered startup (devnet)**: In the devnet startup script, validator N
   waits `(N-1) * 5` seconds before starting the supernode when EVM migration
   is pending. This spreads migrations across ~20 seconds so that by the time
   later validators query the chain, earlier validators have already committed
   their migration records.

**Code locations:**

| Component | File | Key symbol |
| --- | --- | --- |
| Migration notify channel | `p2p/kademlia/dht.go` | `migrationNotify` field |
| Accelerated refresher | `p2p/kademlia/bootstrap.go` | `StartBootstrapRefresher()`, `NotifyEVMMigration()` |
| P2P interface method | `p2p/p2p.go` | `NotifyEVMMigration()` |
| Startup integration | `supernode/cmd/start.go` | `evmMigrationOccurred` flag |

### Key Architectural Decisions

- **SYNC broadcast + polling**: `BROADCAST_MODE_SYNC` only waits for `CheckTx`.
- Local state (keyring, config) is only mutated after `waitForTxConfirmation` polls `GetTx` and confirms block inclusion via `DeliverTx`.
- **`migrationChainClient` interface**: Chain interactions (queries + broadcast) are
  abstracted behind an interface, enabling comprehensive unit testing without a live
  gRPC connection.

## Code Changes

### New Files

| File                                                  | Description                                                                                   |
| ----------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `supernode/cmd/evmigration.go`                      | Core migration logic: chain detection, key validation, dual signing, broadcast, config update |
| `supernode/cmd/evmigration_test.go`                 | Unit tests with mock chain client                                                             |
| `tests/integration/evmigration/evmigration_test.go` | Integration tests for keyring lifecycle, signing protocol, config persistence                 |

### Modified Files

| File                             | Changes                                                                                                                            |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `pkg/keyring/keyring.go`       | Switched to EVM defaults:`DefaultHDPath = "m/44'/60'/0'/0/0"`, `EthSecp256k1Option()`, `evmcryptocodec.RegisterInterfaces()` |
| `pkg/keyring/keyring_test.go`  | Added 10 tests for EVM key creation, derivation, signing, legacy-vs-EVM address differences                                        |
| `pkg/lumera/codec/encoding.go` | Registers `evmcryptocodec` and `evmigrationtypes` interfaces                                                                   |
| `pkg/lumera/interface.go`      | Added `Conn() *grpc.ClientConn` to Client interface                                                                              |
| `pkg/lumera/client.go`         | Implemented `Conn()` method                                                                                                      |
| `pkg/lumera/lumera_mock.go`    | Added `Conn()` to mock client                                                                                                    |
| `pkg/testutil/lumera.go`       | Added `Conn()` to `MockLumeraClient`                                                                                           |
| `supernode/config/config.go`   | Added `EVMKeyName string` field to `SupernodeConfig`                                                                           |
| `supernode/cmd/start.go`       | Integrated `requireEVMChain()` and `ensureLegacyAccountMigrated()` at startup                                                  |
| `go.mod` / `go.sum`          | Added `github.com/cosmos/evm` dependency                                                                                         |

### Key Types and Functions

#### `supernode/cmd/evmigration.go`

```go
// Interface for testability
type migrationChainClient interface {
    MigrationRecord(ctx, legacyAddr)   -> (*QueryMigrationRecordResponse, error)
    MigrationEstimate(ctx, legacyAddr) -> (*QueryMigrationEstimateResponse, error)
    BroadcastMigrationTx(ctx, msg)     -> error
}

// Production implementation
type grpcMigrationClient struct { conn *grpc.ClientConn }

// Core functions
func requireEVMChain(ctx, conn) error
func isLegacyKey(kr, keyName) (bool, error)
func isEthSecp256k1Key(kr, keyName) (bool, error)
func ensureLegacyAccountMigrated(ctx, kr, cfg, chainClient, snModule) error
func broadcastMigrationTx(ctx, conn, msg) error
func waitForTxConfirmation(ctx, txClient, txHash) error
```

#### `pkg/keyring/keyring.go`

```go
const DefaultHDPath = "m/44'/60'/0'/0/0"  // EVM coin type 60

func InitKeyring(cfg)                   // Uses EthSecp256k1Option()
func CreateNewAccount(kr, name)         // Creates eth_secp256k1 key
func RecoverAccountFromMnemonic(kr, name, mnemonic) // Recovers with eth_secp256k1
func DerivePrivKeyFromMnemonic(mnemonic, hdPath)    // Derives eth_secp256k1.PrivKey
```

#### `supernode/config/config.go`

```go
type SupernodeConfig struct {
    KeyName    string `yaml:"key_name"`
    Identity   string `yaml:"identity"`
    EVMKeyName string `yaml:"evm_key_name,omitempty"`  // NEW
    // ...
}
```

## Test Coverage

### Unit Tests (`supernode/cmd/evmigration_test.go`)

**Key type detection (5 tests):**

- `TestIsLegacyKey_WithSecp256k1` — detects legacy key
- `TestIsLegacyKey_WithEthSecp256k1` — rejects EVM key as legacy
- `TestIsLegacyKey_KeyNotFound` — handles missing key
- `TestIsEthSecp256k1Key_WithEVMKey` — detects EVM key
- `TestIsEthSecp256k1Key_WithLegacyKey` — rejects legacy key as EVM

**Early-return / validation (7 tests):**

- `TestEnsureLegacyAccountMigrated_NoMigrationNeeded` — no-op for EVM keys
- `TestEnsureLegacyAccountMigrated_LegacyKeyNoEVMKeyName` — error when evm_key_name missing
- `TestEnsureLegacyAccountMigrated_EVMKeyNotFound` — error when EVM key not in keyring
- `TestEnsureLegacyAccountMigrated_EVMKeyWrongType` — error when EVM key is wrong algorithm
- `TestEnsureLegacyAccountMigrated_AddressCollision` — validates different addresses
- `TestEnsureLegacyAccountMigrated_Idempotent_AlreadyEVM` — no-op when already EVM
- `TestEnsureLegacyAccountMigrated_KeyNameNotFound` — error when key missing

**Already-migrated / rerun (4 tests):**

- `TestEnsureLegacyAccountMigrated_AlreadyMigrated_MatchingAddress` — skip broadcast, clean up
- `TestEnsureLegacyAccountMigrated_AlreadyMigrated_MismatchedAddress` — error on address mismatch
- `TestEnsureLegacyAccountMigrated_AlreadyMigrated_ConfigPersisted` — skip broadcast, verify config file saved to disk
- `TestEnsureLegacyAccountMigrated_MigrationRecordQueryError_ProceedsToMigrate` — resilient to query failures
- `TestEnsureLegacyAccountMigrated_MigrationRecordNilRecord_ProceedsToMigrate` — nil Record field in response proceeds to broadcast

**Validator migration (5 tests):**

- `TestEnsureLegacyAccountMigrated_ValidatorUsesMsgMigrateValidator` — correct message for validators
- `TestEnsureLegacyAccountMigrated_ValidatorFullHappyPath` — full validator flow: MsgMigrateValidator broadcast, key delete, config save, disk persistence
- `TestEnsureLegacyAccountMigrated_NonValidatorUsesMsgClaimLegacyAccount` — correct message for non-validators
- `TestEnsureLegacyAccountMigrated_EstimateErrorFallsBackToNonValidator` — defaults to claim on error
- `TestEnsureLegacyAccountMigrated_EstimateWouldNotSucceed` — blocks on failed estimate

**Failure handling (3 tests):**

- `TestEnsureLegacyAccountMigrated_BroadcastFails` — preserves legacy key on broadcast error
- `TestEnsureLegacyAccountMigrated_BroadcastFails_PreservesFullState` — verifies all local state (key, config, identity) unchanged on DeliverTx failure
- `TestEnsureLegacyAccountMigrated_ConfigSaveFailsAfterKeyDelete` — handles partial failure

**Tx confirmation (`waitForTxConfirmation`) (4 tests):**

- `TestWaitForTxConfirmation_Success` — polls through not-found and nil responses until confirmed
- `TestWaitForTxConfirmation_DeliverTxFailure` — returns error with code/codespace/rawlog on non-zero DeliverTx code
- `TestWaitForTxConfirmation_ContextCancelled` — respects context cancellation
- `TestWaitForTxConfirmation_Timeout` — returns error when context deadline expires

**Supernode verification (2 tests):**

- `TestEnsureLegacyAccountMigrated_SupernodeVerificationFails_NonFatal` — query error is non-fatal, migration completes
- `TestEnsureLegacyAccountMigrated_SupernodeVerificationReturnsNil_NonFatal` — nil supernode result is non-fatal

**Message fields and resilience (2 tests):**

- `TestEnsureLegacyAccountMigrated_MessageFieldsCorrect` — verifies addresses, public keys, and signatures in broadcasted message
- `TestEnsureLegacyAccountMigrated_BothQueriesFail_StillBroadcasts` — both MigrationRecord and MigrationEstimate fail, still broadcasts with defaults

**End-to-end (1 test):**

- `TestEnsureLegacyAccountMigrated_FullHappyPath` — broadcast -> key delete -> config save -> verify

**Keyring / config helpers (4 tests):**

- `TestEnsureLegacyAccountMigrated_ValidationPassesBeforeNetwork`
- `TestKeyDeleteAfterMigration`
- `TestConfigUpdateAfterMigration`
- `TestConfigSaveCreatesFile`

### Keyring Unit Tests (`pkg/keyring/keyring_test.go`)

- `TestGetBech32Address` — bech32 encoding
- `TestDefaultHDPath_IsCoinType60` — verifies coin type 60
- `TestCreateNewAccount_ProducesEthSecp256k1Key` — eth_secp256k1 creation
- `TestRecoverAccountFromMnemonic_ProducesEthSecp256k1Key` — recovery
- `TestRecoverAccountFromMnemonic_Deterministic` — deterministic derivation
- `TestDerivePrivKeyFromMnemonic_ReturnsEthSecp256k1` — private key type
- `TestDerivePrivKeyFromMnemonic_MatchesKeyring` — derivation consistency
- `TestLegacyAndEVMKeys_ProduceDifferentAddresses` — coin type 118 vs 60
- `TestSignBytes_WithEVMKey` — signing
- `TestGetAddress_WithEVMKey` — address retrieval

### Integration Tests (`tests/integration/evmigration/evmigration_test.go`)

- `TestFullMigrationKeyringFlow` — complete lifecycle: create legacy key, import EVM key from same mnemonic, dual-sign payload, delete legacy key, verify EVM key accessible
- `TestConfigPersistenceAfterMigration` — save/reload config, verify identity updated and evm_key_name cleared, verify omitempty in raw YAML
- `TestDualSigningProtocol` — validates exact signing protocol: SHA256 pre-hash for legacy, raw for EVM; verifies cross-protocol signatures don't validate
- `TestMigrationIdempotency` — post-migration state: legacy key gone, EVM key is eth_secp256k1, second migration check is no-op
