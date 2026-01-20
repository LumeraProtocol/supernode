package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"google.golang.org/grpc"
)

type module struct {
	client types.QueryClient
}

func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: types.NewQueryClient(conn),
	}, nil
}

func (m *module) CurrentWindow(ctx context.Context) (*types.QueryCurrentWindowResponse, error) {
	resp, err := m.client.CurrentWindow(ctx, &types.QueryCurrentWindowRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to query current window: %w", err)
	}
	return resp, nil
}

func (m *module) AssignedTargets(ctx context.Context, supernodeAccount string, windowID uint64) (*types.QueryAssignedTargetsResponse, error) {
	supernodeAccount = strings.TrimSpace(supernodeAccount)
	if supernodeAccount == "" {
		return nil, fmt.Errorf("supernodeAccount cannot be empty")
	}

	resp, err := m.client.AssignedTargets(ctx, &types.QueryAssignedTargetsRequest{
		SupernodeAccount: supernodeAccount,
		WindowId:         windowID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query assigned targets: %w", err)
	}
	return resp, nil
}
