package testutil

import (
	"context"

	"github.com/LumeraProtocol/lumera/x/action/v1/types"
	supernodeTypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"

	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
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
	return &authtypes.QueryAccountInfoResponse{}, nil
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

// Add methods as needed based on the actual action_msg.Module interface
// For now, this is a placeholder implementation

// FinalizeCascadeAction implements the required method from action_msg.Module interface
func (m *MockActionMsgModule) FinalizeCascadeAction(ctx context.Context, actionId string, signatures []string) (*action_msg.FinalizeActionResult, error) {
	// Mock implementation returns success with empty result
	return &action_msg.FinalizeActionResult{}, nil
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
	return &supernodeTypes.SuperNode{}, nil
}

func (m *MockSupernodeModule) GetParams(ctx context.Context) (*supernodeTypes.QueryParamsResponse, error) {
	return &supernodeTypes.QueryParamsResponse{}, nil
}

// MockTxModule implements the tx.Module interface for testing
type MockTxModule struct{}

func (m *MockTxModule) BroadcastTx(ctx context.Context, txBytes []byte, mode sdktx.BroadcastMode) (*sdktx.BroadcastTxResponse, error) {
	return &sdktx.BroadcastTxResponse{}, nil
}

func (m *MockTxModule) SimulateTx(ctx context.Context, txBytes []byte) (*sdktx.SimulateResponse, error) {
	return &sdktx.SimulateResponse{}, nil
}

func (m *MockTxModule) GetTx(ctx context.Context, hash string) (*sdktx.GetTxResponse, error) {
	return &sdktx.GetTxResponse{}, nil
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
