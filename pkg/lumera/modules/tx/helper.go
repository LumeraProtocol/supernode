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
	ChainID       string
	Keyring       keyring.Keyring
	KeyName       string
	GasLimit      uint64
	GasAdjustment float64
	GasPadding    uint64
	FeeDenom      string
	GasPrice      string
}

// NewTxHelper creates a new transaction helper with the given configuration
func NewTxHelper(authmod auth.Module, txmod Module, config *TxHelperConfig) *TxHelper {
	txConfig := &TxConfig{
		ChainID:       config.ChainID,
		Keyring:       config.Keyring,
		KeyName:       config.KeyName,
		GasLimit:      config.GasLimit,
		GasAdjustment: config.GasAdjustment,
		GasPadding:    config.GasPadding,
		FeeDenom:      config.FeeDenom,
		GasPrice:      config.GasPrice,
	}

	return &TxHelper{
		authmod: authmod,
		txmod:   txmod,
		config:  txConfig,
	}
}

// NewTxHelperWithDefaults creates a new transaction helper with default configuration
func NewTxHelperWithDefaults(authmod auth.Module, txmod Module, chainID, keyName string, kr keyring.Keyring) *TxHelper {
	config := &TxHelperConfig{
		ChainID:       chainID,
		Keyring:       kr,
		KeyName:       keyName,
		GasLimit:      DefaultGasLimit,
		GasAdjustment: DefaultGasAdjustment,
		GasPadding:    DefaultGasPadding,
		FeeDenom:      DefaultFeeDenom,
		GasPrice:      DefaultGasPrice,
	}

	return NewTxHelper(authmod, txmod, config)
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

		// Run full tx flow
		resp, err := h.ExecuteTransactionWithMsgs(ctx, []types.Msg{msg}, accountInfo)
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
