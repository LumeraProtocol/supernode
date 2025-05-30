package tx

import (
	"context"
	"fmt"
	"strconv"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	lumeracodec "github.com/LumeraProtocol/supernode/pkg/lumera/codec"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

// Default parameters
const (
	DefaultGasLimit      = uint64(200000)
	DefaultGasAdjustment = float64(1.5)
	DefaultGasPadding    = uint64(50000)
	DefaultFeeDenom      = "ulume"
	DefaultGasPrice      = "0.000001" // Price per unit of gas
)

// module implements the Module interface
type module struct {
	client sdktx.ServiceClient
}

// newModule creates a new Transaction module client
func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: sdktx.NewServiceClient(conn),
	}, nil
}

// SimulateTransaction simulates a transaction with given messages and returns gas used
func (m *module) SimulateTransaction(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount, config *TxConfig) (*sdktx.SimulateResponse, error) {
	// Create encoding config
	encCfg := lumeracodec.GetEncodingConfig()

	// Create client context
	clientCtx := client.Context{}.
		WithCodec(encCfg.Codec).
		WithTxConfig(encCfg.TxConfig).
		WithKeyring(config.Keyring).
		WithBroadcastMode("sync")

	// Get the key for public key
	key, err := config.Keyring.Key(config.KeyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get key from keyring: %w", err)
	}

	pubKey, err := key.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	// Use a minimal fee for simulation, just to avoid errors related to fees
	minFee := fmt.Sprintf("1%s", config.FeeDenom)

	// Build unsigned transaction for simulation
	txBuilder, err := tx.Factory{}.
		WithTxConfig(clientCtx.TxConfig).
		WithKeybase(config.Keyring).
		WithAccountNumber(accountInfo.AccountNumber).
		WithSequence(accountInfo.Sequence).
		WithChainID(config.ChainID).
		WithGas(config.GasLimit).
		WithGasAdjustment(config.GasAdjustment).
		WithSignMode(signingtypes.SignMode_SIGN_MODE_DIRECT).
		WithFees(minFee).
		BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to build unsigned tx for simulation: %w", err)
	}

	// Set empty signature for simulation
	txBuilder.SetSignatures(signingtypes.SignatureV2{
		PubKey:   pubKey,
		Data:     &signingtypes.SingleSignatureData{SignMode: signingtypes.SignMode_SIGN_MODE_DIRECT, Signature: nil},
		Sequence: accountInfo.Sequence,
	})

	// Encode transaction for simulation
	txBytes, err := clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction for simulation: %w", err)
	}

	logtrace.Info(ctx, "transaction encoded for simulation", logtrace.Fields{
		"bytesLength": len(txBytes),
	})

	// Simulate transaction
	simReq := &sdktx.SimulateRequest{
		TxBytes: txBytes,
	}

	simRes, err := m.client.Simulate(ctx, simReq)
	if err != nil {
		logtrace.Error(ctx, "simulation error details", logtrace.Fields{
			"error":        err.Error(),
			"errorType":    fmt.Sprintf("%T", err),
			"requestBytes": len(simReq.TxBytes),
		})
		return nil, fmt.Errorf("simulation error: %w", err)
	}

	logtrace.Info(ctx, "simulation response", logtrace.Fields{
		"gasUsed":   simRes.GasInfo.GasUsed,
		"gasWanted": simRes.GasInfo.GasWanted,
	})

	return simRes, nil
}

// BuildAndSignTransaction builds and signs a transaction with the given parameters
func (m *module) BuildAndSignTransaction(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount, gasLimit uint64, fee string, config *TxConfig) ([]byte, error) {
	// Create encoding config
	encCfg := lumeracodec.GetEncodingConfig()

	// Create client context
	clientCtx := client.Context{}.
		WithCodec(encCfg.Codec).
		WithTxConfig(encCfg.TxConfig).
		WithKeyring(config.Keyring).
		WithBroadcastMode("sync")

	// Create transaction factory
	factory := tx.Factory{}.
		WithTxConfig(clientCtx.TxConfig).
		WithKeybase(config.Keyring).
		WithAccountNumber(accountInfo.AccountNumber).
		WithSequence(accountInfo.Sequence).
		WithChainID(config.ChainID).
		WithGas(gasLimit).
		WithGasAdjustment(config.GasAdjustment).
		WithSignMode(signingtypes.SignMode_SIGN_MODE_DIRECT).
		WithFees(fee)

	// Build unsigned transaction
	txBuilder, err := factory.BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to build unsigned tx: %w", err)
	}

	// Sign transaction
	err = tx.Sign(ctx, factory, config.KeyName, txBuilder, true)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	logtrace.Info(ctx, "transaction signed successfully", nil)

	// Encode signed transaction
	txBytes, err := clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction: %w", err)
	}

	return txBytes, nil
}

// BroadcastTransaction broadcasts a signed transaction and returns the result
func (m *module) BroadcastTransaction(ctx context.Context, txBytes []byte) (*sdktx.BroadcastTxResponse, error) {
	// Broadcast transaction
	req := &sdktx.BroadcastTxRequest{
		TxBytes: txBytes,
		Mode:    sdktx.BroadcastMode_BROADCAST_MODE_SYNC,
	}

	resp, err := m.client.BroadcastTx(ctx, req)

	if err != nil {
		logtrace.Error(ctx, "broadcast transaction error", logtrace.Fields{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	return resp, nil
}

// CalculateFee calculates the transaction fee based on gas usage and config
func (m *module) CalculateFee(gasAmount uint64, config *TxConfig) string {
	gasPrice, _ := strconv.ParseFloat(config.GasPrice, 64)
	feeAmount := gasPrice * float64(gasAmount)

	// Ensure we have at least 1 token as fee to meet minimum requirements
	if feeAmount < 1 {
		feeAmount = 1
	}

	return fmt.Sprintf("%.0f%s", feeAmount, config.FeeDenom)
}

// ProcessTransaction handles the complete flow: simulate, build, sign, and broadcast
func (m *module) ProcessTransaction(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount, config *TxConfig) (*sdktx.BroadcastTxResponse, error) {
	// Step 1: Simulate transaction to get gas estimate
	simulatedGas, err := m.SimulateTransaction(ctx, msgs, accountInfo, config)
	if err != nil {
		return nil, fmt.Errorf("simulation failed: %w", err)
	}

	// Step 2: Calculate gas with adjustment and padding
	simulatedGasUsed := simulatedGas.GasInfo.GasUsed
	adjustedGas := uint64(float64(simulatedGasUsed) * config.GasAdjustment)
	gasToUse := adjustedGas + config.GasPadding

	// Step 3: Calculate fee based on adjusted gas
	fee := m.CalculateFee(gasToUse, config)

	logtrace.Info(ctx, "using simulated gas and calculated fee", logtrace.Fields{
		"simulatedGas": simulatedGasUsed,
		"adjustedGas":  gasToUse,
		"fee":          fee,
	})

	// Step 4: Build and sign transaction
	txBytes, err := m.BuildAndSignTransaction(ctx, msgs, accountInfo, gasToUse, fee, config)
	if err != nil {
		return nil, fmt.Errorf("failed to build and sign transaction: %w", err)
	}

	// Step 5: Broadcast transaction
	result, err := m.BroadcastTransaction(ctx, txBytes)
	if err != nil {
		return result, fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	return result, nil
}
