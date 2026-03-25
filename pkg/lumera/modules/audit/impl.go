package audit

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/audit/v1/types"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc"
)

type module struct {
	client types.QueryClient
}

func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}
	return &module{client: types.NewQueryClient(conn)}, nil
}

func (m *module) GetParams(ctx context.Context) (*types.QueryParamsResponse, error) {
	resp, err := m.client.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get audit params: %w", err)
	}
	return resp, nil
}

func (m *module) GetEpochAnchor(ctx context.Context, epochID uint64) (*types.QueryEpochAnchorResponse, error) {
	resp, err := m.client.EpochAnchor(ctx, &types.QueryEpochAnchorRequest{EpochId: epochID})
	if err != nil {
		return nil, fmt.Errorf("failed to get epoch anchor: %w", err)
	}
	return resp, nil
}

func (m *module) GetCurrentEpochAnchor(ctx context.Context) (*types.QueryCurrentEpochAnchorResponse, error) {
	resp, err := m.client.CurrentEpochAnchor(ctx, &types.QueryCurrentEpochAnchorRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get current epoch anchor: %w", err)
	}
	return resp, nil
}

func (m *module) GetCurrentEpoch(ctx context.Context) (*types.QueryCurrentEpochResponse, error) {
	resp, err := m.client.CurrentEpoch(ctx, &types.QueryCurrentEpochRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get current epoch: %w", err)
	}
	return resp, nil
}

func (m *module) GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*types.QueryAssignedTargetsResponse, error) {
	resp, err := m.client.AssignedTargets(ctx, &types.QueryAssignedTargetsRequest{
		SupernodeAccount: supernodeAccount,
		EpochId:          epochID,
		FilterByEpochId:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get assigned targets: %w", err)
	}
	return resp, nil
}

func (m *module) GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*types.QueryEpochReportResponse, error) {
	resp, err := m.client.EpochReport(ctx, &types.QueryEpochReportRequest{
		EpochId:          epochID,
		SupernodeAccount: supernodeAccount,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get epoch report: %w", err)
	}
	return resp, nil
}

func (m *module) GetStorageChallengeReports(ctx context.Context, supernodeAccount string, epochID uint64) (*types.QueryStorageChallengeReportsResponse, error) {
	page := &querytypes.PageRequest{Limit: 1000}
	all := make([]types.StorageChallengeReport, 0)
	var lastPagination *querytypes.PageResponse

	for {
		resp, err := m.client.StorageChallengeReports(ctx, &types.QueryStorageChallengeReportsRequest{
			SupernodeAccount: supernodeAccount,
			EpochId:          epochID,
			FilterByEpochId:  true,
			Pagination:       page,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get storage challenge reports: %w", err)
		}
		if resp == nil {
			break
		}

		all = append(all, resp.Reports...)
		lastPagination = resp.Pagination
		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}

		page = &querytypes.PageRequest{
			Key:   resp.Pagination.NextKey,
			Limit: 1000,
		}
	}

	return &types.QueryStorageChallengeReportsResponse{
		Reports:    all,
		Pagination: lastPagination,
	}, nil
}
