package action

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	client types.QueryClient
}

// newModule creates a new Action module client
func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: types.NewQueryClient(conn),
	}, nil
}

// GetAction fetches an action by ID
func (m *module) GetAction(ctx context.Context, actionID string) (*types.QueryGetActionResponse, error) {
	resp, err := m.client.GetAction(ctx, &types.QueryGetActionRequest{
		ActionID: actionID,
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// GetActionFee calculates fee for processing data with given size
func (m *module) GetActionFee(ctx context.Context, dataSize string) (*types.QueryGetActionFeeResponse, error) {
	resp, err := m.client.GetActionFee(ctx, &types.QueryGetActionFeeRequest{
		DataSize: dataSize,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get action fee: %w", err)
	}

	return resp, nil
}

// GetParams fetches the action module parameters
func (m *module) GetParams(ctx context.Context) (*types.QueryParamsResponse, error) {
	resp, err := m.client.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get action params: %w", err)
	}

	return resp, nil
}

// ListActionsBySuperNode lists actions assigned to a specific supernode.
func (m *module) ListActionsBySuperNode(ctx context.Context, superNodeAddress string) (*types.QueryListActionsBySuperNodeResponse, error) {
	var all []*types.Action
	var nextKey []byte
	for {
		resp, err := m.client.ListActionsBySuperNode(ctx, &types.QueryListActionsBySuperNodeRequest{
			SuperNodeAddress: superNodeAddress,
			Pagination: &query.PageRequest{
				Key:   nextKey,
				Limit: 100,
			},
		})
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return &types.QueryListActionsBySuperNodeResponse{Actions: all}, nil
		}
		all = append(all, resp.Actions...)
		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			resp.Actions = all
			return resp, nil
		}
		nextKey = resp.Pagination.NextKey
	}
}
