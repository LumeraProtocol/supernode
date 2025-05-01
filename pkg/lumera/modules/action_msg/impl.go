package action_msg

import (
	"context"
	"encoding/json"
	"fmt"

	actiontypes "github.com/LumeraProtocol/supernode/gen/lumera/action/types"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

// Default gas parameters
const (
	defaultGasLimit      = uint64(200000)
	defaultMinGasLimit   = uint64(100000)
	defaultMaxGasLimit   = uint64(1000000)
	defaultGasAdjustment = float64(3.0)
	defaultGasPadding    = uint64(50000)
)

// module implements the Module interface
type module struct {
	conn          *grpc.ClientConn
	client        actiontypes.MsgClient
	kr            keyring.Keyring
	keyName       string
	chainID       string
	gasLimit      uint64
	minGasLimit   uint64
	maxGasLimit   uint64
	gasAdjustment float64
	gasPadding    uint64
}

// newModule creates a new ActionMsg module client
func newModule(conn *grpc.ClientConn, kr keyring.Keyring, keyName string, chainID string) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	if kr == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}

	if keyName == "" {
		return nil, fmt.Errorf("key name cannot be empty")
	}

	if chainID == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}

	return &module{
		conn:          conn,
		client:        actiontypes.NewMsgClient(conn),
		kr:            kr,
		keyName:       keyName,
		chainID:       chainID,
		gasLimit:      defaultGasLimit,
		minGasLimit:   defaultMinGasLimit,
		maxGasLimit:   defaultMaxGasLimit,
		gasAdjustment: defaultGasAdjustment,
		gasPadding:    defaultGasPadding,
	}, nil
}

// FinalizeCascadeAction finalizes a CASCADE action with the given parameters
func (m *module) FinalizeCascadeAction(
	ctx context.Context,
	actionId string,
	rqIdsIds []string,
	rqIdsOti []byte,
) (*FinalizeActionResult, error) {
	// Basic validation
	if actionId == "" {
		return nil, fmt.Errorf("action ID cannot be empty")
	}
	if len(rqIdsIds) == 0 {
		return nil, fmt.Errorf("rq_ids_ids cannot be empty for cascade action")
	}
	if len(rqIdsOti) == 0 {
		return nil, fmt.Errorf("rq_ids_oti cannot be empty for cascade action")
	}

	// Get creator address from keyring
	key, err := m.kr.Key(m.keyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get key from keyring: %w", err)
	}

	addr, err := key.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key: %w", err)
	}
	creator := addr.String()

	logtrace.Info(ctx, "finalize action started", logtrace.Fields{"creator": creator})

	// Create CASCADE metadata
	metadata := map[string]interface{}{
		"rq_ids_ids": rqIdsIds,
		"rq_ids_oti": rqIdsOti,
	}

	// Convert metadata to JSON
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Create the message
	msg := &actiontypes.MsgFinalizeAction{
		Creator:    creator,
		ActionId:   actionId,
		ActionType: "CASCADE",
		Metadata:   string(metadataJSON),
	}

	// Create encoding config
	encCfg := makeEncodingConfig()

	// Get account info for signing
	accInfo, err := m.getAccountInfo(ctx, creator)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	logtrace.Info(ctx, "account info retrieved", logtrace.Fields{"accountNumber": accInfo.AccountNumber})

	// Create client context with keyring
	clientCtx := client.Context{}.
		WithCodec(encCfg.Codec).
		WithTxConfig(encCfg.TxConfig).
		WithKeyring(m.kr).
		WithBroadcastMode("sync")

	// Simulate transaction to get gas estimate
	txBuilder, err := tx.Factory{}.
		WithTxConfig(clientCtx.TxConfig).
		WithKeybase(m.kr).
		WithAccountNumber(accInfo.AccountNumber).
		WithSequence(accInfo.Sequence).
		WithChainID(m.chainID).
		WithGas(m.gasLimit).
		WithGasAdjustment(m.gasAdjustment).
		WithSignMode(signingtypes.SignMode_SIGN_MODE_DIRECT).
		BuildUnsignedTx(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to build unsigned tx for simulation: %w", err)
	}
	pubKey, err := key.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}
	txBuilder.SetSignatures(signingtypes.SignatureV2{
		PubKey:   pubKey, // your signing pubkey
		Data:     &signingtypes.SingleSignatureData{SignMode: signingtypes.SignMode_SIGN_MODE_DIRECT, Signature: nil},
		Sequence: accInfo.Sequence,
	})
	simulatedGas, err := m.simulateTx(ctx, clientCtx, txBuilder)
	if err != nil {
		return nil, fmt.Errorf("simulation failed: %w", err)
	}

	adjustedGas := uint64(float64(simulatedGas) * m.gasAdjustment)
	gasToUse := adjustedGas + m.gasPadding

	// Apply gas bounds
	if gasToUse > m.maxGasLimit {
		gasToUse = m.maxGasLimit
	}

	logtrace.Info(ctx, "using simulated gas", logtrace.Fields{"simulatedGas": simulatedGas, "adjustedGas": gasToUse})

	// Create transaction factory with final gas
	factory := tx.Factory{}.
		WithTxConfig(clientCtx.TxConfig).
		WithKeybase(m.kr).
		WithAccountNumber(accInfo.AccountNumber).
		WithSequence(accInfo.Sequence).
		WithChainID(m.chainID).
		WithGas(gasToUse).
		WithGasAdjustment(m.gasAdjustment).
		WithSignMode(signingtypes.SignMode_SIGN_MODE_DIRECT)

	// Build and sign transaction
	txBuilder, err = factory.BuildUnsignedTx(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to build unsigned tx: %w", err)
	}

	err = tx.Sign(ctx, factory, m.keyName, txBuilder, true)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	logtrace.Info(ctx, "transaction signed successfully", nil)

	// Broadcast transaction
	txBytes, err := clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction: %w", err)
	}

	resp, err := m.broadcastTx(ctx, txBytes)
	if err != nil {
		return &FinalizeActionResult{
			Success: false,
			TxHash:  "",
		}, fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	logtrace.Info(ctx, "transaction broadcast success", logtrace.Fields{"txHash": resp.TxHash})

	return &FinalizeActionResult{
		TxHash:  resp.TxHash,
		Code:    resp.Code,
		Success: true,
	}, nil
}

// Helper function to simulate transaction and return gas used
func (m *module) simulateTx(ctx context.Context, clientCtx client.Context, txBuilder client.TxBuilder) (uint64, error) {
	// First, let's see what's in the txBuilder
	tx := txBuilder.GetTx()
	logtrace.Info(ctx, "transaction for simulation", logtrace.Fields{
		"messages": fmt.Sprintf("%v", tx.GetMsgs()),
		"fee":      fmt.Sprintf("%v", tx.GetFee()),
		"gas":      tx.GetGas(),
	})

	txBytes, err := clientCtx.TxConfig.TxEncoder()(tx)
	if err != nil {
		return 0, fmt.Errorf("failed to encode transaction for simulation: %w", err)
	}

	logtrace.Info(ctx, "transaction encoded for simulation", logtrace.Fields{
		"bytesLength": len(txBytes),
	})

	// Create gRPC client for tx service
	txClient := txtypes.NewServiceClient(m.conn)

	// Simulate transaction
	simReq := &txtypes.SimulateRequest{
		TxBytes: txBytes,
	}

	// Check if we have the tx in the request too
	if simReq.Tx != nil {
		logtrace.Info(ctx, "simulation request has tx field", logtrace.Fields{
			"txFieldPresent": true,
		})
	}

	logtrace.Info(ctx, "sending simulation request", logtrace.Fields{
		"requestBytes": len(simReq.TxBytes),
		"requestType":  fmt.Sprintf("%T", simReq),
	})

	simRes, err := txClient.Simulate(ctx, simReq)
	if err != nil {
		logtrace.Error(ctx, "simulation error details", logtrace.Fields{
			"error":        err.Error(),
			"errorType":    fmt.Sprintf("%T", err),
			"requestBytes": len(simReq.TxBytes),
		})
		return 0, fmt.Errorf("simulation error: %w", err)
	}

	logtrace.Info(ctx, "simulation response", logtrace.Fields{
		"gasUsed":   simRes.GasInfo.GasUsed,
		"gasWanted": simRes.GasInfo.GasWanted,
	})

	return simRes.GasInfo.GasUsed, nil
}

// Helper function to broadcast transaction
func (m *module) broadcastTx(ctx context.Context, txBytes []byte) (*TxResponse, error) {
	// Create gRPC client for tx service
	txClient := txtypes.NewServiceClient(m.conn)

	// Broadcast transaction
	req := &txtypes.BroadcastTxRequest{
		TxBytes: txBytes,
		Mode:    txtypes.BroadcastMode_BROADCAST_MODE_SYNC,
	}

	resp, err := txClient.BroadcastTx(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("broadcast failed: %w", err)
	}

	if resp.TxResponse.Code != 0 {
		return nil, fmt.Errorf("transaction failed (code %d): %s",
			resp.TxResponse.Code, resp.TxResponse.RawLog)
	}

	return &TxResponse{
		TxHash: resp.TxResponse.TxHash,
		Code:   resp.TxResponse.Code,
		RawLog: resp.TxResponse.RawLog,
	}, nil
}

// Helper function to get account info
func (m *module) getAccountInfo(ctx context.Context, address string) (*AccountInfo, error) {
	// Create gRPC client for auth service
	authClient := authtypes.NewQueryClient(m.conn)

	// Query account info
	req := &authtypes.QueryAccountRequest{
		Address: address,
	}

	resp, err := authClient.Account(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	// Unmarshal account
	var account authtypes.AccountI
	err = m.getEncodingConfig().InterfaceRegistry.UnpackAny(resp.Account, &account)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack account: %w", err)
	}

	// Convert to BaseAccount
	baseAcc, ok := account.(*authtypes.BaseAccount)
	if !ok {
		return nil, fmt.Errorf("received account is not a BaseAccount")
	}

	return &AccountInfo{
		AccountNumber: baseAcc.AccountNumber,
		Sequence:      baseAcc.Sequence,
	}, nil
}

// makeEncodingConfig creates an EncodingConfig for transaction handling
func makeEncodingConfig() EncodingConfig {
	amino := codec.NewLegacyAmino()

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	authtypes.RegisterInterfaces(interfaceRegistry)
	actiontypes.RegisterInterfaces(interfaceRegistry)

	marshaler := codec.NewProtoCodec(interfaceRegistry)
	txConfig := authtx.NewTxConfig(marshaler, authtx.DefaultSignModes)

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             marshaler,
		TxConfig:          txConfig,
		Amino:             amino,
	}
}

// getEncodingConfig returns the module's encoding config
func (m *module) getEncodingConfig() EncodingConfig {
	return makeEncodingConfig()
}

// EncodingConfig specifies the concrete encoding types to use
type EncodingConfig struct {
	InterfaceRegistry types.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

// AccountInfo holds account information for transaction signing
type AccountInfo struct {
	AccountNumber uint64
	Sequence      uint64
}

// TxResponse holds transaction response information
type TxResponse struct {
	TxHash string
	Code   uint32
	RawLog string
}
