package action_msg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LumeraProtocol/supernode/gen/lumera/action/types"
	actiontypes "github.com/LumeraProtocol/supernode/gen/lumera/action/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	conn     *grpc.ClientConn
	client   types.MsgClient
	kr       keyring.Keyring
	keyName  string
	chainID  string
	nodeAddr string
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

	// Extract node address from connection
	nodeAddr := conn.Target()

	return &module{
		conn:     conn,
		client:   types.NewMsgClient(conn),
		kr:       kr,
		keyName:  keyName,
		chainID:  chainID,
		nodeAddr: nodeAddr,
	}, nil
}

// FinalizeCascadeAction finalizes a CASCADE action with the given parameters
func (m *module) FinalizeCascadeAction(
	ctx context.Context,
	actionId string,
	rqIdsIds []string,
	rqIdsOti []string,
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
	msg := &types.MsgFinalizeAction{
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

	// Create client context with keyring
	clientCtx := client.Context{}.
		WithCodec(encCfg.Codec).
		WithTxConfig(encCfg.TxConfig).
		WithKeyring(m.kr).
		WithBroadcastMode("sync")

	// Create transaction factory
	factory := tx.Factory{}.
		WithTxConfig(clientCtx.TxConfig).
		WithKeybase(m.kr).
		WithAccountNumber(accInfo.AccountNumber).
		WithSequence(accInfo.Sequence).
		WithChainID(m.chainID).
		WithGas(200000).
		WithGasAdjustment(1.5).
		WithSignMode(signingtypes.SignMode_SIGN_MODE_DIRECT)

	// Build unsigned transaction
	txBuilder, err := factory.BuildUnsignedTx(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to build unsigned tx: %w", err)
	}

	// Simulate transaction to get accurate gas estimation
	gasInfo, err := m.simulateTx(ctx, clientCtx, txBuilder)
	if err != nil {
		return nil, fmt.Errorf("failed to simulate transaction: %w", err)
	}

	// Update gas amount based on simulation
	factory = factory.WithGas(gasInfo + 10000)
	txBuilder, err = factory.BuildUnsignedTx(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to rebuild unsigned tx: %w", err)
	}

	// Sign transaction
	err = tx.Sign(ctx, factory, m.keyName, txBuilder, true)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Broadcast transaction
	txBytes, err := clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction: %w", err)
	}

	resp, err := m.broadcastTx(ctx, txBytes)
	if err != nil {
		return &FinalizeActionResult{
			Success: false,
		}, fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	return &FinalizeActionResult{
		TxHash:  resp.TxHash,
		Success: true,
	}, nil
}

// Helper function to simulate transaction and return gas used
func (m *module) simulateTx(ctx context.Context, clientCtx client.Context, txBuilder client.TxBuilder) (uint64, error) {
	txBytes, err := clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return 0, err
	}
	// Create gRPC client for tx service
	txClient := txtypes.NewServiceClient(m.conn)

	// Simulate transaction
	simReq := &txtypes.SimulateRequest{
		TxBytes: txBytes,
	}

	simRes, err := txClient.Simulate(ctx, simReq)
	if err != nil {
		return 0, err
	}

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
		return nil, err
	}

	if resp.TxResponse.Code != 0 {
		return nil, fmt.Errorf("transaction failed: %s", resp.TxResponse.RawLog)
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
		return nil, fmt.Errorf("failed to unmarshal account: %w", err)
	}

	return &AccountInfo{
		AccountNumber: account.GetAccountNumber(),
		Sequence:      account.GetSequence(),
	}, nil
}

// makeEncodingConfig creates an EncodingConfig for transaction handling
func makeEncodingConfig() EncodingConfig {
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
	}
}

// getEncodingConfig returns the module's encoding config
func (m *module) getEncodingConfig() EncodingConfig {
	return makeEncodingConfig()
}

// EncodingConfig specifies the concrete encoding types to use
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
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
