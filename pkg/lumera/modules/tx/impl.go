package tx

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	lumeracodec "github.com/LumeraProtocol/supernode/v2/pkg/lumera/codec"

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
	// DefaultGasPrice is the default min gas price in denom units (e.g., ulume)
	// Set to 0.025 to match chain defaults where applicable.
	DefaultGasPrice = "0.025"
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
	// Create encoding config and client context
	encCfg := lumeracodec.GetEncodingConfig()
	clientCtx := client.Context{}.
		WithCodec(encCfg.Codec).
		WithTxConfig(encCfg.TxConfig).
		WithKeyring(config.Keyring)

	// Create a transaction factory with Gas set to 0 to trigger auto-estimation
	// and add a minimal fee to pass the mempool check during simulation.
	txf := tx.Factory{}.
		WithTxConfig(clientCtx.TxConfig).
		WithKeybase(config.Keyring).
		WithAccountNumber(accountInfo.AccountNumber).
		WithSequence(accountInfo.Sequence).
		WithChainID(config.ChainID).
		WithGas(0). // Setting Gas to 0 is the key for estimation
		WithSignMode(signingtypes.SignMode_SIGN_MODE_DIRECT).
		WithFees(fmt.Sprintf("1%s", config.FeeDenom)) // Minimal fee for simulation

	// Build the unsigned transaction once
	txb, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to build unsigned tx for simulation: %w", err)
	}

	// Create a dummy signature to account for its size in the gas estimation
	key, err := config.Keyring.Key(config.KeyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get key from keyring: %w", err)
	}
	pubKey, err := key.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}
	sig := signingtypes.SignatureV2{
		PubKey:   pubKey,
		Data:     &signingtypes.SingleSignatureData{SignMode: txf.SignMode(), Signature: nil},
		Sequence: accountInfo.Sequence,
	}
	if err := txb.SetSignatures(sig); err != nil {
		return nil, fmt.Errorf("failed to set dummy signature: %w", err)
	}

	// Encode the transaction for simulation
	txBytes, err := clientCtx.TxConfig.TxEncoder()(txb.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction for simulation: %w", err)
	}

	// Simulate the transaction
	simRes, err := m.client.Simulate(ctx, &sdktx.SimulateRequest{TxBytes: txBytes})
	if err != nil {
		return nil, fmt.Errorf("simulation error: %w", err)
	}

	logtrace.Info(ctx, fmt.Sprintf("simulation complete | gasUsed=%d", simRes.GasInfo.GasUsed), nil)
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
		logtrace.Error(ctx, fmt.Sprintf("broadcast transaction error | error=%s", err.Error()), nil)
		return nil, fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	// If the chain returns a non-zero code, surface it as an error with context
	if resp != nil && resp.TxResponse != nil && resp.TxResponse.Code != 0 {
		return resp, fmt.Errorf(
			"tx failed: code=%d codespace=%s height=%d gas_wanted=%d gas_used=%d raw_log=%s",
			resp.TxResponse.Code,
			resp.TxResponse.Codespace,
			resp.TxResponse.Height,
			resp.TxResponse.GasWanted,
			resp.TxResponse.GasUsed,
			resp.TxResponse.RawLog,
		)
	}

	return resp, nil
}

// CalculateFee calculates the transaction fee based on gas usage and config
func (m *module) CalculateFee(gasAmount uint64, config *TxConfig) string {
	// Determine gas price (numeric) and denom. Accept both plain number (e.g., "0.025")
	// and dec-coin format (e.g., "0.025ulume").
	var (
		gasPriceF float64
		denom     = config.FeeDenom
	)

	gp := config.GasPrice

	// First try: parse as decimal coin if suffix present
	if gp != "" {
		// Attempt dec-coin parse (handles "0.025ulume")
		if decCoin, err := types.ParseDecCoin(gp); err == nil {
			// Amount is a decimal string; convert to float64 for calculation
			if f, errf := strconv.ParseFloat(decCoin.Amount.String(), 64); errf == nil {
				gasPriceF = f
			}
			if denom == "" {
				denom = decCoin.Denom
			}
		} else {
			// Fallback: parse as plain float (e.g., "0.025")
			if f, err2 := strconv.ParseFloat(gp, 64); err2 == nil {
				gasPriceF = f
			}
		}
	}

	// Fallbacks if not provided or parsing failed
	if gasPriceF <= 0 {
		if f, err := strconv.ParseFloat(DefaultGasPrice, 64); err == nil {
			gasPriceF = f
		} else {
			gasPriceF = 0.0
		}
	}
	if denom == "" {
		denom = DefaultFeeDenom
	}

	// Calculate fee and always round up to meet chain minimums
	feeFloat := gasPriceF * float64(gasAmount)
	feeInt := uint64(math.Ceil(feeFloat))

	// Ensure we have at least 1 unit of fee to meet minimal requirements
	feeInt = max(feeInt, 1)

	return fmt.Sprintf("%d%s", feeInt, denom)
}

// ProcessTransaction handles the complete flow: simulate, build, sign, and broadcast
func (m *module) ProcessTransaction(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount, config *TxConfig) (*sdktx.BroadcastTxResponse, error) {
	if err := validateTxConfig(config); err != nil {
		return nil, fmt.Errorf("invalid tx config: %w", err)
	}
	// Step 1: Simulate transaction to get gas estimate
	simRes, err := m.SimulateTransaction(ctx, msgs, accountInfo, config)
	if err != nil {
		return nil, fmt.Errorf("simulation failed: %w", err)
	}
	if simRes == nil || simRes.GasInfo == nil || simRes.GasInfo.GasUsed == 0 {
		return nil, fmt.Errorf("invalid simulation result: empty or zero gas used")
	}

	// Step 2: Calculate gas with adjustment and padding
	simulatedGasUsed := simRes.GasInfo.GasUsed
	adjustedGas := uint64(float64(simulatedGasUsed) * config.GasAdjustment)
	gasToUse := adjustedGas + config.GasPadding

	// Step 3: Calculate fee based on adjusted gas
	fee := m.CalculateFee(gasToUse, config)

	logtrace.Info(ctx, fmt.Sprintf("using simulated gas and calculated fee | simulatedGas=%d adjustedGas=%d fee=%s", simulatedGasUsed, gasToUse, fee), nil)

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

// validateTxConfig validates critical fields of TxConfig for safe processing.
func validateTxConfig(config *TxConfig) error {
	if config == nil {
		return fmt.Errorf("tx config cannot be nil")
	}
	if config.ChainID == "" {
		return fmt.Errorf("chainID cannot be empty")
	}
	if config.Keyring == nil {
		return fmt.Errorf("keyring cannot be nil")
	}
	if config.KeyName == "" {
		return fmt.Errorf("key name cannot be empty")
	}
	if config.GasAdjustment <= 0 {
		return fmt.Errorf("gas adjustment must be > 0 (got %v)", config.GasAdjustment)
	}
	// If a gas price is provided, validate its format. Accept dec-coin or plain decimal.
	if gp := config.GasPrice; gp != "" {
		if decCoin, err := types.ParseDecCoin(gp); err == nil {
			if config.FeeDenom != "" && config.FeeDenom != decCoin.Denom {
				return fmt.Errorf("fee denom %q does not match gas price denom %q", config.FeeDenom, decCoin.Denom)
			}
		} else if _, err2 := strconv.ParseFloat(gp, 64); err2 != nil {
			return fmt.Errorf("invalid gas price format %q; use '0.025' or '0.025ulume'", gp)
		}
	}
	return nil
}
