package lumera

import (
	"context"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types/tx"
	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
)

type DummyClient interface {
	MasterNodesExtra(ctx context.Context) (SuperNodeAddressInfos, error)
	MasterNodesTop(ctx context.Context) (SuperNodeAddressInfos, error)
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

func (c *Client) GetKeyring() keyring.Keyring {
	return c.cosmosSdk.Keyring
}

type SuperNodeAddressInfo struct {
	ExtKey     string
	ExtAddress string
	ExtP2P     string
}

type SuperNodeAddressInfos []SuperNodeAddressInfo

type LumeraNetwork struct {
	nodes SuperNodeAddressInfos // list of SuperNodes address info
}

// NewLumeraNetwork creates a new mock client with pre-configured nodes
func NewLumeraNetwork(nodeConfig ltc.LumeraAddresses) *LumeraNetwork {
	nodes := make([]SuperNodeAddressInfo, len(nodeConfig))
	for i, cfg := range nodeConfig {
		nodes[i] = SuperNodeAddressInfo{
			ExtKey:     cfg.Identity, 	 // LumeraID of the node
			ExtAddress: cfg.HostPort(),  // IP:Port for general communication
			ExtP2P:     cfg.HostPort(),  // Same address for P2P - in real network this might be different
		}
	}

	return &LumeraNetwork{
		nodes: nodes,
	}
}

func (m *LumeraNetwork) MasterNodesExtra(ctx context.Context) (SuperNodeAddressInfos, error) {
	return m.nodes, nil
}

func (m *LumeraNetwork) MasterNodesTop(ctx context.Context) (SuperNodeAddressInfos, error) {
	return m.nodes, nil
}

func (m *LumeraNetwork) Sign(_ context.Context, _ []byte, _ string, _ string, _ string) (string, error) {
	// For demo, just return a dummy signature
	return "dummy-signature", nil
}

func (m *LumeraNetwork) Verify(_ context.Context, _ []byte, _ string, _ string, _ string) (bool, error) {
	// For demo, always verify as true
	return true, nil
}
