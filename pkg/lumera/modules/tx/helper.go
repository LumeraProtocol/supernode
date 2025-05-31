package tx

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/auth"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// TxHelper provides a simplified interface for modules to handle transactions
// This helper encapsulates common transaction patterns and reduces boilerplate
type TxHelper struct {
	authmod auth.Module
	txmod   Module
	config  *TxConfig
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

// ExecuteTransaction is a convenience method that handles the complete transaction flow
// for a single message. It gets account info, creates the message, and processes the transaction.
func (h *TxHelper) ExecuteTransaction(ctx context.Context, msgCreator func(creator string) (types.Msg, error)) (*sdktx.BroadcastTxResponse, error) {
	// Step 1: Get creator address from keyring
	key, err := h.config.Keyring.Key(h.config.KeyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get key from keyring: %w", err)
	}

	addr, err := key.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key: %w", err)
	}
	creator := addr.String()

	// Step 2: Get account info
	accInfoRes, err := h.authmod.AccountInfoByAddress(ctx, creator)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	// Step 3: Create the message using the provided creator function
	msg, err := msgCreator(creator)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	// Step 4: Process transaction
	return h.ExecuteTransactionWithMsgs(ctx, []types.Msg{msg}, accInfoRes.Info)
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

// UpdateConfig allows updating the transaction configuration
func (h *TxHelper) UpdateConfig(config *TxHelperConfig) {
	h.config = &TxConfig{
		ChainID:       config.ChainID,
		Keyring:       config.Keyring,
		KeyName:       config.KeyName,
		GasLimit:      config.GasLimit,
		GasAdjustment: config.GasAdjustment,
		GasPadding:    config.GasPadding,
		FeeDenom:      config.FeeDenom,
		GasPrice:      config.GasPrice,
	}
}

// GetConfig returns the current transaction configuration
func (h *TxHelper) GetConfig() *TxConfig {
	return h.config
}
