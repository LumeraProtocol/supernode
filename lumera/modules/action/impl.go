package action

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/action/types"
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
		return nil, fmt.Errorf("failed to get action: %w", err)
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
