package supernode

import (
	"context"
	"fmt"
	"sort"

	"github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"

	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	client types.QueryClient
}

// newModule creates a new SuperNode module client
func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: types.NewQueryClient(conn),
	}, nil
}

// GetTopSuperNodesForBlock gets the top supernodes for a specific block height
func (m *module) GetTopSuperNodesForBlock(ctx context.Context, blockHeight uint64) (*types.QueryGetTopSuperNodesForBlockResponse, error) {
	resp, err := m.client.GetTopSuperNodesForBlock(ctx, &types.QueryGetTopSuperNodesForBlockRequest{
		BlockHeight: int32(blockHeight),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get top supernodes: %w", err)
	}

	return resp, nil
}

// GetSuperNode gets a supernode by account address
func (m *module) GetSuperNode(ctx context.Context, address string) (*types.QueryGetSuperNodeResponse, error) {
	resp, err := m.client.GetSuperNode(ctx, &types.QueryGetSuperNodeRequest{
		ValidatorAddress: address,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}

	return resp, nil
}

func (m *module) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*types.SuperNode, error) {
	resp, err := m.client.GetSuperNodeBySuperNodeAddress(ctx, &types.QueryGetSuperNodeBySuperNodeAddressRequest{
		SupernodeAddress: address,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}

	return resp.Supernode, nil
}

// GetParams fetches the supernode module parameters
func (m *module) GetParams(ctx context.Context) (*types.QueryParamsResponse, error) {
	resp, err := m.client.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode params: %w", err)
	}

	return resp, nil
}

func Exists(nodes []*types.SuperNode, snAccAddress string) bool {
	for _, sn := range nodes {
		if sn.SupernodeAccount == snAccAddress {
			return true
		}
	}
	return false
}

func GetLatestIP(supernode *types.SuperNode) (string, error) {
	if len(supernode.PrevIpAddresses) == 0 {
		return "", errors.Errorf("no ip history exists for the supernode")
	}
	sort.Slice(supernode.PrevIpAddresses, func(i, j int) bool {
		return supernode.PrevIpAddresses[i].GetHeight() > supernode.PrevIpAddresses[j].GetHeight()
	})

	return supernode.PrevIpAddresses[0].Address, nil
}

// GetSupernodeWithLatestAddress gets a supernode by account address and returns comprehensive info
func (m *module) GetSupernodeWithLatestAddress(ctx context.Context, address string) (*SuperNodeInfo, error) {
	supernode, err := m.GetSupernodeBySupernodeAddress(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}

	// Get latest IP address
	var latestAddress string
	if len(supernode.PrevIpAddresses) > 0 {
		sort.Slice(supernode.PrevIpAddresses, func(i, j int) bool {
			return supernode.PrevIpAddresses[i].GetHeight() > supernode.PrevIpAddresses[j].GetHeight()
		})
		latestAddress = supernode.PrevIpAddresses[0].Address
	}

	// Get latest state
	var currentState string
	if len(supernode.States) > 0 {
		sort.Slice(supernode.States, func(i, j int) bool {
			return supernode.States[i].Height > supernode.States[j].Height
		})
		currentState = supernode.States[0].State.String()
	}

	return &SuperNodeInfo{
		SupernodeAccount: supernode.SupernodeAccount,
		ValidatorAddress: supernode.ValidatorAddress,
		P2PPort:          supernode.P2PPort,
		LatestAddress:    latestAddress,
		CurrentState:     currentState,
	}, nil
}
