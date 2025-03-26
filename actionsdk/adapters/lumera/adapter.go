package lumera

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/action/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/types"
	lumeraclient "github.com/LumeraProtocol/supernode/pkg/lumera"
)

type Client interface {
	GetAction(ctx context.Context, actionID string) (Action, error)
	GetSupernodes(ctx context.Context, height int64) ([]Supernode, error)
}

// Adapter adapts the lumera.Client to our Client interface
type Adapter struct {
	client lumeraclient.Client
}

// NewAdapter creates a new adapter for the lumera.Client
func NewAdapter(client lumeraclient.Client) Client {
	return &Adapter{
		client: client,
	}
}

// GetAction retrieves action information from the blockchain
func (a *Adapter) GetAction(ctx context.Context, actionID string) (Action, error) {
	resp, err := a.client.Action().GetAction(ctx, actionID)
	if err != nil {
		return Action{}, fmt.Errorf("failed to get action: %w", err)
	}

	// Transform the response to our simplified Action type
	return toSdkAction(resp), nil
}

// GetSupernodes retrieves a list of top supernodes at a given height
func (a *Adapter) GetSupernodes(ctx context.Context, height int64) ([]Supernode, error) {
	resp, err := a.client.SuperNode().GetTopSuperNodesForBlock(ctx, uint64(height))
	if err != nil {
		return nil, fmt.Errorf("failed to get supernodes: %w", err)
	}

	// Transform the response to our simplified Supernode type
	return toSdkSupernodes(resp), nil
}

// Helper functions to transform between types

func toSdkAction(resp *types.QueryGetActionResponse) Action {
	return Action{
		ID:             resp.Action.ActionID,
		State:          ACTION_STATE(resp.Action.State),
		Height:         int64(resp.Action.BlockHeight),
		ExpirationTime: resp.Action.ExpirationTime,
	}
}

func toSdkSupernodes(resp *sntypes.QueryGetTopSuperNodesForBlockResponse) []Supernode {
	var result []Supernode
	for _, sn := range resp.Supernodes {
		ipAddress, err := getLatestIP(sn)
		if err != nil {
			continue
		}

		if sn.SupernodeAccount == "" {
			continue
		}

		if sn.States[0].State.String() != string(SUPERNODE_STATE_ACTIVE) {
			continue
		}

		result = append(result, Supernode{
			CosmosAddress: sn.SupernodeAccount,
			GrpcEndpoint:  ipAddress,
			State:         SUPERNODE_STATE_ACTIVE,
		})
	}
	return result
}

// getLatestIP is a simplified version of the GetLatestIP function in supernode module
func getLatestIP(supernode *sntypes.SuperNode) (string, error) {
	if len(supernode.PrevIpAddresses) == 0 {
		return "", fmt.Errorf("no ip history exists for the supernode")
	}
	// Just take the first one for simplicity
	return supernode.PrevIpAddresses[0].Address, nil
}
