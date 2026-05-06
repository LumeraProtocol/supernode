package audit

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/cosmos/cosmos-sdk/types/query"
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

func (m *module) GetEpochReportsByReporter(ctx context.Context, reporterAccount string, epochID uint64) (*types.QueryEpochReportsByReporterResponse, error) {
	resp, err := m.client.EpochReportsByReporter(ctx, &types.QueryEpochReportsByReporterRequest{
		SupernodeAccount: reporterAccount,
		EpochId:          epochID,
		FilterByEpochId:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get epoch reports by reporter: %w", err)
	}
	return resp, nil
}

func (m *module) GetHealOp(ctx context.Context, healOpID uint64) (*types.QueryHealOpResponse, error) {
	resp, err := m.client.HealOp(ctx, &types.QueryHealOpRequest{
		HealOpId: healOpID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get heal op: %w", err)
	}
	return resp, nil
}

func (m *module) GetHealOpsByStatus(ctx context.Context, status types.HealOpStatus, pagination *query.PageRequest) (*types.QueryHealOpsByStatusResponse, error) {
	resp, err := m.client.HealOpsByStatus(ctx, &types.QueryHealOpsByStatusRequest{
		Status:     status,
		Pagination: pagination,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get heal ops by status: %w", err)
	}
	return resp, nil
}

func (m *module) GetHealOpsByTicket(ctx context.Context, ticketID string, pagination *query.PageRequest) (*types.QueryHealOpsByTicketResponse, error) {
	resp, err := m.client.HealOpsByTicket(ctx, &types.QueryHealOpsByTicketRequest{
		TicketId:   ticketID,
		Pagination: pagination,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get heal ops by ticket: %w", err)
	}
	return resp, nil
}
