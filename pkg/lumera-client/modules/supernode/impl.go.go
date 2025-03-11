package supernode

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/supernode/types"
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
