package action_msg

import (
	"context"
	"fmt"
	"strconv"

	actionapi "github.com/LumeraProtocol/lumera/api/lumera/action"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/types"
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
	"google.golang.org/protobuf/encoding/protojson"
)

// Default parameters
const (
	defaultGasLimit      = uint64(200000)
	defaultGasAdjustment = float64(1.5)
	defaultGasPadding    = uint64(50000)
	defaultFeeDenom      = "ulume"
	defaultGasPrice      = "0.000001" // Price per unit of gas
)

// module implements the Module interface
type module struct {
	conn          *grpc.ClientConn
	client        actiontypes.MsgClient
	kr            keyring.Keyring
	keyName       string
	chainID       string
	gasLimit      uint64
	gasAdjustment float64
	gasPadding    uint64
	feeDenom      string
	gasPrice      string
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
		gasAdjustment: defaultGasAdjustment,
		gasPadding:    defaultGasPadding,
		feeDenom:      defaultFeeDenom,
		gasPrice:      defaultGasPrice,
	}, nil
}

// calculateFee calculates the transaction fee based on gas usage
func (m *module) calculateFee(gasAmount uint64) string {
	gasPrice, _ := strconv.ParseFloat(m.gasPrice, 64)
	feeAmount := gasPrice * float64(gasAmount)

	// Ensure we have at least 1 token as fee to meet minimum requirements
	if feeAmount < 1 {
		feeAmount = 1
	}

	return fmt.Sprintf("%.0f%s", feeAmount, m.feeDenom)
}

// FinalizeCascadeAction finalizes a CASCADE action with the given parameters
func (m *module) FinalizeCascadeAction(
	ctx context.Context,
	actionId string,
	rqIdsIds []string,
) (*FinalizeActionResult, error) {
	// Basic validation
	if actionId == "" {
		return nil, fmt.Errorf("action ID cannot be empty")
	}
	if len(rqIdsIds) == 0 {
		return nil, fmt.Errorf("rq_ids_ids cannot be empty for cascade action")
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
	cascadeMeta := actionapi.CascadeMetadata{
		RqIdsIds: rqIdsIds,
	}

	// Convert metadata to JSON instead of binary protobuf
	metadataBytes, err := protojson.Marshal(&cascadeMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata to JSON: %w", err)
	}

	// Create the message
	msg := &actiontypes.MsgFinalizeAction{
		Creator:    creator,
		ActionId:   actionId,
		ActionType: "CASCADE",
		Metadata:   string(metadataBytes),
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

	// Use a minimal fee for simulation
	minFee := fmt.Sprintf("1%s", m.feeDenom)

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
		WithFees(minFee).
		BuildUnsignedTx(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to build unsigned tx for simulation: %w", err)
	}

	pubKey, err := key.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	txBuilder.SetSignatures(signingtypes.SignatureV2{
		PubKey:   pubKey,
		Data:     &signingtypes.SingleSignatureData{SignMode: signingtypes.SignMode_SIGN_MODE_DIRECT, Signature: nil},
		Sequence: accInfo.Sequence,
	})

	simulatedGas, err := m.simulateTx(ctx, clientCtx, txBuilder)
	if err != nil {
		return nil, fmt.Errorf("simulation failed: %w", err)
	}

	// Calculate gas with adjustment and padding
	adjustedGas := uint64(float64(simulatedGas) * m.gasAdjustment)
	gasToUse := adjustedGas + m.gasPadding

	// Calculate fee based on adjusted gas
	fee := m.calculateFee(gasToUse)
	logtrace.Info(ctx, "using simulated gas and calculated fee", logtrace.Fields{
		"simulatedGas": simulatedGas,
		"adjustedGas":  gasToUse,
		"fee":          fee,
	})

	// Create transaction factory with final gas and calculated fee
	factory := tx.Factory{}.
		WithTxConfig(clientCtx.TxConfig).
		WithKeybase(m.kr).
		WithAccountNumber(accInfo.AccountNumber).
		WithSequence(accInfo.Sequence).
		WithChainID(m.chainID).
		WithGas(gasToUse).
		WithGasAdjustment(m.gasAdjustment).
		WithSignMode(signingtypes.SignMode_SIGN_MODE_DIRECT).
		WithFees(fee)

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
