package base

import (
	"context"
	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"github.com/cosmos/cosmos-sdk/client"

	"github.com/cosmos/cosmos-sdk/types/tx"
)

type DummyClient interface {
	MasterNodesExtra(ctx context.Context) (MasterNodes, error)
	MasterNodesTop(ctx context.Context) (MasterNodes, error)
	Sign(ctx context.Context, data []byte, pastelID string, passphrase string, algorithm string) (signature string, err error)
	Verify(ctx context.Context, data []byte, signature string, pastelID string, algorithm string) (bool, error)
}

type Client struct {
	cosmosSdk        client.Context
	txClient         tx.ServiceClient
	tendermintClient tendermintv1beta1.ServiceClient
}

type CosmosBase interface {
	GetLatestBlock(ctx context.Context) (Block, error)
	GetBlockByHeight(ctx context.Context, height int64) (Block, error)
	BroadcastTx(ctx context.Context, req BroadcastRequest) (BroadcastResponse, error)
	GetTx(ctx context.Context, req GetTxRequest) (GetTxResponse, error)
}

func NewTendermintClient(opts ...Option) (CosmosBase, error) {
	c := &Client{}

	// Apply all options to the client context
	c.WithOptions(opts...)

	return c, nil
}

type MasterNode struct {
	ExtKey     string
	ExtAddress string
	ExtP2P     string
}

type MasterNodes []MasterNode

type LumeraClient struct {
	nodes []MasterNode // list of nodes in our demo network
}

type LumeraClientConfig []struct {
	Address  string
	LumeraID string
}

// NewLumeraClient creates a new mock client with pre-configured nodes
func NewLumeraClient(nodeConfig LumeraClientConfig) *LumeraClient {
	nodes := make([]MasterNode, len(nodeConfig))
	for i, cfg := range nodeConfig {
		nodes[i] = MasterNode{
			ExtKey:     cfg.LumeraID, // LumeraID of the node
			ExtAddress: cfg.Address,  // IP:Port for general communication
			ExtP2P:     cfg.Address,  // Same address for P2P - in real network this might be different
		}
	}

	return &LumeraClient{
		nodes: nodes,
	}
}

func (m *LumeraClient) MasterNodesExtra(ctx context.Context) (MasterNodes, error) {
	return m.nodes, nil
}

func (m *LumeraClient) MasterNodesTop(ctx context.Context) (MasterNodes, error) {
	return m.nodes, nil
}

func (m *LumeraClient) Sign(_ context.Context, _ []byte, _ string, _ string, _ string) (string, error) {
	// For demo, just return a dummy signature
	return "dummy-signature", nil
}

func (m *LumeraClient) Verify(_ context.Context, _ []byte, _ string, _ string, _ string) (bool, error) {
	// For demo, always verify as true
	return true, nil
}
