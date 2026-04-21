package tx

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

const (
	sequenceMismatchMaxAttempts = 3
	sequenceMismatchRetryStep   = 500 * time.Millisecond
)

func sleepSequenceMismatchBackoff(ctx context.Context, attempt int) {
	timer := time.NewTimer(time.Duration(attempt) * sequenceMismatchRetryStep)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		return
	}
}

// TxHelper provides a simplified interface for modules to handle transactions
// This helper encapsulates common transaction patterns and reduces boilerplate
type TxHelper struct {
	authmod auth.Module
	txmod   Module
	config  *TxConfig

	mu sync.Mutex

	accountNumber uint64
	nextSequence  uint64
	seqInit       bool
}

// TxHelperConfig holds configuration for creating a TxHelper
type TxHelperConfig struct {
	ChainID                  string
	Keyring                  keyring.Keyring
	KeyName                  string
	GasLimit                 uint64
	GasAdjustment            float64
	GasAdjustmentMultiplier  float64
	GasAdjustmentMaxAttempts int
	GasPadding               uint64
	FeeDenom                 string
	GasPrice                 string
}

// NewTxHelper creates a new transaction helper with the given configuration.
// Any zero-valued field in config falls back to the package-level Default* value,
// so callers can pass partially-populated configs.
func NewTxHelper(authmod auth.Module, txmod Module, config *TxHelperConfig) *TxHelper {
	applied := applyTxHelperDefaults(config)

	txConfig := &TxConfig{
		ChainID:                  applied.ChainID,
		Keyring:                  applied.Keyring,
		KeyName:                  applied.KeyName,
		GasLimit:                 applied.GasLimit,
		GasAdjustment:            applied.GasAdjustment,
		GasAdjustmentMultiplier:  applied.GasAdjustmentMultiplier,
		GasAdjustmentMaxAttempts: applied.GasAdjustmentMaxAttempts,
		GasPadding:               applied.GasPadding,
		FeeDenom:                 applied.FeeDenom,
		GasPrice:                 applied.GasPrice,
	}

	return &TxHelper{
		authmod: authmod,
		txmod:   txmod,
		config:  txConfig,
	}
}

// NewTxHelperWithDefaults creates a new transaction helper with default configuration.
// Preserved for backward compatibility with callers that only know chainID/keyName/keyring.
func NewTxHelperWithDefaults(authmod auth.Module, txmod Module, chainID, keyName string, kr keyring.Keyring) *TxHelper {
	return NewTxHelper(authmod, txmod, &TxHelperConfig{
		ChainID: chainID,
		Keyring: kr,
		KeyName: keyName,
	})
}

// applyTxHelperDefaults fills zero-valued fields in cfg with package defaults
// and returns a new, fully-populated config. cfg itself is not mutated.
func applyTxHelperDefaults(cfg *TxHelperConfig) TxHelperConfig {
	if cfg == nil {
		cfg = &TxHelperConfig{}
	}
	out := *cfg
	if out.GasLimit == 0 {
		out.GasLimit = DefaultGasLimit
	}
	if out.GasAdjustment <= 0 {
		out.GasAdjustment = DefaultGasAdjustment
	}
	if out.GasAdjustmentMultiplier <= 1.0 {
		// Must be strictly >1 for escalation to do anything.
		out.GasAdjustmentMultiplier = DefaultGasAdjustmentMultiplier
	}
	if out.GasAdjustmentMaxAttempts <= 0 {
		out.GasAdjustmentMaxAttempts = DefaultGasAdjustmentMaxAttempts
	}
	if out.GasAdjustmentMaxAttempts > 10 {
		// hard cap as a safety net to prevent runaway fee spend.
		out.GasAdjustmentMaxAttempts = 10
	}
	if out.GasPadding == 0 {
		out.GasPadding = DefaultGasPadding
	}
	if strings.TrimSpace(out.FeeDenom) == "" {
		out.FeeDenom = DefaultFeeDenom
	}
	if strings.TrimSpace(out.GasPrice) == "" {
		out.GasPrice = DefaultGasPrice
	}
	return out
}

// cloneTxConfig returns a shallow copy safe for per-attempt mutation.
// Keyring is interface-valued; we share the pointer (read-only usage).
func cloneTxConfig(c *TxConfig) *TxConfig {
	if c == nil {
		return nil
	}
	copy := *c
	return &copy
}

func (h *TxHelper) ExecuteTransaction(
	ctx context.Context,
	msgCreator func(creator string) (types.Msg, error),
) (*sdktx.BroadcastTxResponse, error) {

	h.mu.Lock()
	defer h.mu.Unlock()

	key, err := h.config.Keyring.Key(h.config.KeyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get key from keyring: %w", err)
	}

	addr, err := key.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address: %w", err)
	}
	creator := addr.String()

	msg, err := msgCreator(creator)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	if !h.seqInit {
		accInfoRes, err := h.authmod.AccountInfoByAddress(ctx, creator)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch account info: %w", err)
		}
		if accInfoRes == nil || accInfoRes.Info == nil {
			return nil, fmt.Errorf("empty account info response for creator %s", creator)
		}

		h.accountNumber = accInfoRes.Info.AccountNumber
		h.nextSequence = accInfoRes.Info.Sequence
		h.seqInit = true
	}

	for attempt := 1; attempt <= sequenceMismatchMaxAttempts; attempt++ {
		usedSequence := h.nextSequence

		accountInfo := &authtypes.BaseAccount{
			AccountNumber: h.accountNumber,
			Sequence:      usedSequence,
			Address:       creator,
		}

		// Run the inner attempt with OOG-retry escalation on gas_adjustment.
		// Sequence is *not* bumped between OOG retries because a SYNC-rejected
		// CheckTx (the OOG path we detect here) does not consume the sequence.
		resp, err := executeWithOOGRetry(
			ctx,
			h.config,
			func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
				return h.txmod.ProcessTransaction(ctx, []types.Msg{msg}, accountInfo, cfg)
			},
		)
		if err == nil {
			h.nextSequence++
			return resp, nil
		}

		// Check if this is a sequence mismatch error
		if !isSequenceMismatch(err) {
			return resp, err // unrelated error → bail out (preserve response for debugging)
		}

		expectedSeq, ok := parseExpectedSequence(err)
		if ok && expectedSeq > h.nextSequence {
			h.nextSequence = expectedSeq
		} else if !ok {
			// Best-effort resync if the error format didn't contain expected/got.
			// Never decrement local state.
			accInfoRes, err2 := h.authmod.AccountInfoByAddress(ctx, creator)
			if err2 == nil && accInfoRes != nil && accInfoRes.Info != nil {
				h.accountNumber = accInfoRes.Info.AccountNumber
				h.nextSequence = max(h.nextSequence, accInfoRes.Info.Sequence)
			}
		}

		// If retry unavailable, bubble error
		if attempt == sequenceMismatchMaxAttempts {
			fields := logtrace.Fields{
				"attempt":       attempt,
				"used_sequence": usedSequence,
				"error":         err.Error(),
			}
			if ok {
				fields["expected_sequence"] = expectedSeq
			}
			logtrace.Warn(ctx, "transaction sequence mismatch", fields)

			return resp, fmt.Errorf("sequence mismatch after retry (%d attempts): %w", sequenceMismatchMaxAttempts, err)
		}

		sleepSequenceMismatchBackoff(ctx, attempt)
	}

	return nil, fmt.Errorf("unreachable state in ExecuteTransaction")
}

// executeWithOOGRetry runs fn with progressively larger GasAdjustment on
// out-of-gas errors. cfg is cloned per attempt so callers are unaffected by
// the mutation of GasAdjustment. Non-OOG errors abort the loop immediately
// (bail semantics preserved for sequence mismatch, signature errors, etc).
//
// Emits a structured "tx_oog_retry" log line on every retry that Datadog can
// facet on.
func executeWithOOGRetry(
	ctx context.Context,
	baseCfg *TxConfig,
	fn func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error),
) (*sdktx.BroadcastTxResponse, error) {
	if baseCfg == nil {
		return nil, fmt.Errorf("tx config cannot be nil")
	}

	maxAttempts := baseCfg.GasAdjustmentMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultGasAdjustmentMaxAttempts
	}
	multiplier := baseCfg.GasAdjustmentMultiplier
	if multiplier <= 1.0 {
		multiplier = DefaultGasAdjustmentMultiplier
	}

	current := cloneTxConfig(baseCfg)
	initialAdj := current.GasAdjustment

	var (
		resp *sdktx.BroadcastTxResponse
		err  error
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err = fn(current)
		if err == nil {
			if attempt > 1 {
				logtrace.Info(ctx, "tx succeeded after OOG retry", logtrace.Fields{
					"metric":                    "tx_oog_retry",
					"attempt":                   attempt,
					"attempts_total":            attempt,
					"initial_gas_adjustment":    initialAdj,
					"final_gas_adjustment":      current.GasAdjustment,
					"gas_adjustment_multiplier": multiplier,
					"outcome":                   "success",
				})
			}
			return resp, nil
		}
		if !isOutOfGas(err) {
			// Non-OOG errors are not retried here; caller may retry at a
			// higher level (e.g. sequence-mismatch loop in ExecuteTransaction).
			return resp, err
		}
		if attempt >= maxAttempts {
			logtrace.Warn(ctx, "tx out-of-gas: max retries exhausted", logtrace.Fields{
				"metric":                    "tx_oog_retry",
				"attempt":                   attempt,
				"attempts_total":            attempt,
				"initial_gas_adjustment":    initialAdj,
				"final_gas_adjustment":      current.GasAdjustment,
				"gas_adjustment_multiplier": multiplier,
				"outcome":                   "exhausted",
				"error":                     err.Error(),
			})
			return resp, fmt.Errorf("out of gas after %d attempts (final gas_adjustment=%.3f): %w",
				attempt, current.GasAdjustment, err)
		}
		// Escalate for next attempt. Clone to avoid mutating caller state.
		next := cloneTxConfig(current)
		next.GasAdjustment = current.GasAdjustment * multiplier
		logtrace.Info(ctx, "tx out-of-gas: retrying with higher gas_adjustment", logtrace.Fields{
			"metric":                    "tx_oog_retry",
			"attempt":                   attempt,
			"next_attempt":              attempt + 1,
			"max_attempts":              maxAttempts,
			"prev_gas_adjustment":       current.GasAdjustment,
			"new_gas_adjustment":        next.GasAdjustment,
			"initial_gas_adjustment":    initialAdj,
			"gas_adjustment_multiplier": multiplier,
			"outcome":                   "retrying",
			"error":                     err.Error(),
		})
		current = next
	}
	// Unreachable given the loop above, but be explicit.
	return resp, err
}

func isSequenceMismatch(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "incorrect account sequence") ||
		strings.Contains(msg, "account sequence mismatch") ||
		strings.Contains(msg, "wrong sequence")
}

func parseExpectedSequence(err error) (uint64, bool) {
	if err == nil {
		return 0, false
	}

	msg := strings.ToLower(err.Error())
	idx := strings.Index(msg, "expected ")
	if idx == -1 {
		return 0, false
	}

	var expected, got uint64
	if _, scanErr := fmt.Sscanf(msg[idx:], "expected %d, got %d", &expected, &got); scanErr == nil {
		return expected, true
	}

	return 0, false
}

// ExecuteTransactionWithMsgs processes a transaction with pre-created messages and account info
func (h *TxHelper) ExecuteTransactionWithMsgs(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount) (*sdktx.BroadcastTxResponse, error) {
	return h.txmod.ProcessTransaction(ctx, msgs, accountInfo, h.config)
}

// GetCreatorAddress returns the creator address for the configured key
func (h *TxHelper) GetCreatorAddress() (string, error) {
	key, err := h.config.Keyring.Key(h.config.KeyName)
	if err != nil {
		return "", fmt.Errorf("failed to get key from keyring: %w", err)
	}

	addr, err := key.GetAddress()
	if err != nil {
		return "", fmt.Errorf("failed to get address from key: %w", err)
	}

	return addr.String(), nil
}

// GetAccountInfo gets account information for the configured key
func (h *TxHelper) GetAccountInfo(ctx context.Context) (*authtypes.BaseAccount, error) {
	creator, err := h.GetCreatorAddress()
	if err != nil {
		return nil, err
	}

	accInfoRes, err := h.authmod.AccountInfoByAddress(ctx, creator)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	return accInfoRes.Info, nil
}

func (h *TxHelper) UpdateConfig(config *TxHelperConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.config == nil {
		h.config = &TxConfig{}
	}

	if config.ChainID != "" {
		h.config.ChainID = config.ChainID
	}
	if config.Keyring != nil {
		h.config.Keyring = config.Keyring
	}
	if config.KeyName != "" {
		h.config.KeyName = config.KeyName
	}
	if config.GasLimit != 0 {
		h.config.GasLimit = config.GasLimit
	}
	if config.GasAdjustment != 0 {
		h.config.GasAdjustment = config.GasAdjustment
	}
	if config.GasAdjustmentMultiplier > 1.0 {
		h.config.GasAdjustmentMultiplier = config.GasAdjustmentMultiplier
	}
	if config.GasAdjustmentMaxAttempts > 0 {
		h.config.GasAdjustmentMaxAttempts = config.GasAdjustmentMaxAttempts
	}
	if config.GasPadding != 0 {
		h.config.GasPadding = config.GasPadding
	}
	if config.FeeDenom != "" {
		h.config.FeeDenom = config.FeeDenom
	}
	if config.GasPrice != "" {
		h.config.GasPrice = config.GasPrice
	}
}

// GetConfig returns the current transaction configuration
func (h *TxHelper) GetConfig() *TxConfig {
	return h.config
}

// Simulate runs an offline simulation for the provided messages using the
// configured tx settings and given account info. Useful for pre-flight checks.
func (h *TxHelper) Simulate(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount) (*sdktx.SimulateResponse, error) {
	return h.txmod.SimulateTransaction(ctx, msgs, accountInfo, h.config)
}
