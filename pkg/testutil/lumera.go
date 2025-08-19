package testutil

import (
	"context"

	"github.com/LumeraProtocol/lumera/x/action/v1/types"
	supernodeTypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"

	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// MockLumeraClient implements the lumera.Client interface for testing purposes
type MockLumeraClient struct {
	authMod      *MockAuthModule
	actionMod    *MockActionModule
	actionMsgMod *MockActionMsgModule
	supernodeMod *MockSupernodeModule
	txMod        *MockTxModule
	nodeMod      *MockNodeModule
	kr           keyring.Keyring
	addresses    []string // Store node addresses for testing
}

// NewMockLumeraClient creates a new mock Lumera client for testing
func NewMockLumeraClient(kr keyring.Keyring, addresses []string) (lumera.Client, error) {
	actionMod := &MockActionModule{}
	actionMsgMod := &MockActionMsgModule{}
	supernodeMod := &MockSupernodeModule{addresses: addresses}
	txMod := &MockTxModule{}
	nodeMod := &MockNodeModule{}

	return &MockLumeraClient{
		authMod:      &MockAuthModule{},
		actionMod:    actionMod,
		actionMsgMod: actionMsgMod,
		supernodeMod: supernodeMod,
		txMod:        txMod,
		nodeMod:      nodeMod,
		kr:           kr,
		addresses:    addresses,
	}, nil
}

// Auth returns the Auth module client
func (c *MockLumeraClient) Auth() auth.Module {
	return c.authMod
}

// Action returns the Action module client
func (c *MockLumeraClient) Action() action.Module {
	return c.actionMod
}

// ActionMsg returns the ActionMsg module client
func (c *MockLumeraClient) ActionMsg() action_msg.Module {
	return c.actionMsgMod
}

// SuperNode returns the SuperNode module client
func (c *MockLumeraClient) SuperNode() supernode.Module {
	return c.supernodeMod
}

// Tx returns the Transaction module client
func (c *MockLumeraClient) Tx() tx.Module {
	return c.txMod
}

// Node returns the Node module client
func (c *MockLumeraClient) Node() node.Module {
	return c.nodeMod
}

// Close closes all connections
func (c *MockLumeraClient) Close() error {
	return nil
}

// MockAuthModule implements the auth.Module interface for testing
type MockAuthModule struct{}

// AccountInfoByAddress mocks the behavior of retrieving account info by address
func (m *MockAuthModule) AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error) {
	// Mock response: Return an empty response for testing, modify as needed
	return &authtypes.QueryAccountInfoResponse{
		Info: &authtypes.BaseAccount{Address: addr},
	}, nil
}

// Verify mocks the behavior of verifying data with a given account address
func (m *MockAuthModule) Verify(ctx context.Context, accAddress string, data, signature []byte) error {
	// In the mock implementation, we just return nil to indicate success
	return nil
}

// MockActionModule implements the action.Module interface for testing
type MockActionModule struct{}

func (m *MockActionModule) GetAction(ctx context.Context, actionID string) (*types.QueryGetActionResponse, error) {
	return &types.QueryGetActionResponse{}, nil
}

func (m *MockActionModule) GetActionFee(ctx context.Context, dataSize string) (*types.QueryGetActionFeeResponse, error) {
	return &types.QueryGetActionFeeResponse{}, nil
}

func (m *MockActionModule) GetParams(ctx context.Context) (*types.QueryParamsResponse, error) {
	return &types.QueryParamsResponse{}, nil
}

// MockActionMsgModule implements the action_msg.Module interface for testing
type MockActionMsgModule struct{}

// RequestAction mocks the behavior of requesting an action.
// Adjust the signature and return values as needed to match the actual interface.
func (m *MockActionMsgModule) RequestAction(ctx context.Context, req *types.MsgRequestAction) (*sdktx.BroadcastTxResponse, error) {
	// Mock implementation returns success with empty result
	return &sdktx.BroadcastTxResponse{}, nil
}

// RequesAction is a stub to satisfy the action_msg.Module interface in case of typo in interface definition.
func (m *MockActionMsgModule) RequesAction(ctx context.Context, arg1, arg2, arg3, arg4 string) (*sdktx.BroadcastTxResponse, error) {
	// Mock implementation returns success with empty result
	return &sdktx.BroadcastTxResponse{}, nil
}

// FinalizeCascadeAction implements the required method from action_msg.Module interface
func (m *MockActionMsgModule) FinalizeCascadeAction(ctx context.Context, actionId string, signatures []string) (*sdktx.BroadcastTxResponse, error) {
	// Mock implementation returns success with empty result
	return &sdktx.BroadcastTxResponse{}, nil
}

// MockSupernodeModule implements the supernode.Module interface for testing
type MockSupernodeModule struct {
	addresses []string
}

func (m *MockSupernodeModule) GetTopSuperNodesForBlock(ctx context.Context, blockHeight uint64) (*supernodeTypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return &supernodeTypes.QueryGetTopSuperNodesForBlockResponse{}, nil
}

func (m *MockSupernodeModule) GetSuperNode(ctx context.Context, address string) (*supernodeTypes.QueryGetSuperNodeResponse, error) {
	return &supernodeTypes.QueryGetSuperNodeResponse{}, nil
}

func (m *MockSupernodeModule) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*supernodeTypes.SuperNode, error) {
	return &supernodeTypes.SuperNode{
		SupernodeAccount: address,
	}, nil
}

func (m *MockSupernodeModule) GetParams(ctx context.Context) (*supernodeTypes.QueryParamsResponse, error) {
	return &supernodeTypes.QueryParamsResponse{}, nil
}

// MockTxModule implements the tx.Module interface for testing
type MockTxModule struct{}

// SimulateTransaction simulates a transaction with given messages and returns gas used
func (m *MockTxModule) SimulateTransaction(ctx context.Context, msgs []sdktypes.Msg, accountInfo *authtypes.BaseAccount, config *tx.TxConfig) (*sdktx.SimulateResponse, error) {
	// Mock implementation returns empty simulation response
	return &sdktx.SimulateResponse{}, nil
}

// BuildAndSignTransaction builds and signs a transaction with the given parameters
func (m *MockTxModule) BuildAndSignTransaction(ctx context.Context, msgs []sdktypes.Msg, accountInfo *authtypes.BaseAccount, gasLimit uint64, fee string, config *tx.TxConfig) ([]byte, error) {
	// Mock implementation returns empty tx bytes
	return []byte{}, nil
}

// BroadcastTransaction mocks the behavior of broadcasting a transaction
func (m *MockTxModule) BroadcastTransaction(ctx context.Context, txBytes []byte) (*sdktx.BroadcastTxResponse, error) {
	// Mock implementation returns an empty response for testing
	return &sdktx.BroadcastTxResponse{}, nil
}

// CalculateFee calculates the transaction fee based on gas usage and config
func (m *MockTxModule) CalculateFee(gasAmount uint64, config *tx.TxConfig) string {
	// Mock implementation returns empty fee string
	return ""
}

// ProcessTransaction handles the complete flow: simulate, build, sign, and broadcast
func (m *MockTxModule) ProcessTransaction(ctx context.Context, msgs []sdktypes.Msg, accountInfo *authtypes.BaseAccount, config *tx.TxConfig) (*sdktx.BroadcastTxResponse, error) {
	// Mock implementation returns empty broadcast response
	return &sdktx.BroadcastTxResponse{}, nil
}

// MockNodeModule implements the node.Module interface for testing
type MockNodeModule struct{}

// Sign implements node.Module.
func (m *MockNodeModule) Sign(snAccAddress string, data []byte) (signature []byte, err error) {
	panic("unimplemented")
}

// Verify implements node.Module.
func (m *MockNodeModule) Verify(accAddress string, data []byte, signature []byte) (err error) {
	panic("unimplemented")
}

func (m *MockNodeModule) GetLatestBlock(ctx context.Context) (*cmtservice.GetLatestBlockResponse, error) {
	return &cmtservice.GetLatestBlockResponse{
		SdkBlock: &cmtservice.Block{},
	}, nil
}

func (m *MockNodeModule) GetBlockByHeight(ctx context.Context, height int64) (*cmtservice.GetBlockByHeightResponse, error) {
	return &cmtservice.GetBlockByHeightResponse{}, nil
}

func (m *MockNodeModule) GetNodeInfo(ctx context.Context) (*cmtservice.GetNodeInfoResponse, error) {
	return &cmtservice.GetNodeInfoResponse{}, nil
}

func (m *MockNodeModule) GetSyncing(ctx context.Context) (*cmtservice.GetSyncingResponse, error) {
	return &cmtservice.GetSyncingResponse{}, nil
}

func (m *MockNodeModule) GetLatestValidatorSet(ctx context.Context) (*cmtservice.GetLatestValidatorSetResponse, error) {
	return &cmtservice.GetLatestValidatorSetResponse{}, nil
}

func (m *MockNodeModule) GetValidatorSetByHeight(ctx context.Context, height int64) (*cmtservice.GetValidatorSetByHeightResponse, error) {
	return &cmtservice.GetValidatorSetByHeightResponse{}, nil
}
