package testutil

import (
	"context"

	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/LumeraProtocol/lumera/x/action/types"
	supernodeTypes "github.com/LumeraProtocol/lumera/x/supernode/types"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"
)

// MockLumeraClient implements the lumera.Client interface for testing purposes
type MockLumeraClient struct {
	actionMod    *MockActionModule
	supernodeMod *MockSupernodeModule
	txMod        *MockTxModule
	nodeMod      *MockNodeModule
	kr           keyring.Keyring
	addresses    []string // Store node addresses for testing
}

// NewMockLumeraClient creates a new mock Lumera client for testing
func NewMockLumeraClient(kr keyring.Keyring, addresses []string) (lumera.Client, error) {
	actionMod := &MockActionModule{}
	supernodeMod := &MockSupernodeModule{addresses: addresses}
	txMod := &MockTxModule{}
	nodeMod := &MockNodeModule{}

	return &MockLumeraClient{
		actionMod:    actionMod,
		supernodeMod: supernodeMod,
		txMod:        txMod,
		nodeMod:      nodeMod,
		kr:           kr,
		addresses:    addresses,
	}, nil
}

// Action returns the Action module client
func (c *MockLumeraClient) Action() action.Module {
	return c.actionMod
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

// MockActionModule implements the action.Module interface for testing
type MockActionModule struct{}

func (m *MockActionModule) GetAction(ctx context.Context, actionID string) (*types.QueryGetActionResponse, error) {
	return &types.QueryGetActionResponse{}, nil
}

func (m *MockActionModule) GetActionFee(ctx context.Context, dataSize string) (*types.QueryGetActionFeeResponse, error) {
	return &types.QueryGetActionFeeResponse{}, nil
}

// MockSupernodeModule implements the supernode.Module interface for testing
type MockSupernodeModule struct {
	addresses []string
}

func (m *MockSupernodeModule) GetTopSuperNodesForBlock(ctx context.Context, blockHeight uint64) (*supernodeTypes.QueryGetTopSuperNodesForBlockResponse, error) {
	// Create supernodes with the actual node addresses supplied in the test
	supernodes := make([]*supernodeTypes.SuperNode, 0, len(m.addresses))

	for i, addr := range m.addresses {
		if i >= 2 { // Only use first couple for bootstrap
			break
		}

		supernode := &supernodeTypes.SuperNode{
			SupernodeAccount: addr, // Use the real account address for testing
			PrevIpAddresses: []*supernodeTypes.IPAddressHistory{
				{
					Address: "127.0.0.1:900" + string('0'+i),
					Height:  10,
				},
			},
		}
		supernodes = append(supernodes, supernode)
	}

	return &supernodeTypes.QueryGetTopSuperNodesForBlockResponse{
		Supernodes: supernodes,
	}, nil
}

func (m *MockSupernodeModule) GetSuperNode(ctx context.Context, address string) (*supernodeTypes.QueryGetSuperNodeResponse, error) {
	return &supernodeTypes.QueryGetSuperNodeResponse{}, nil
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

func (m *MockNodeModule) GetLatestBlock(ctx context.Context) (*cmtservice.GetLatestBlockResponse, error) {
	return &cmtservice.GetLatestBlockResponse{
		SdkBlock: &cmtservice.Block{
			Header: cmtservice.Header{
				Height: 100,
			},
		},
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
