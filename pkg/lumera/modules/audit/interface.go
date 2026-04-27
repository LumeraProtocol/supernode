package audit

import (
	"context"

	"github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc"
)

// Module defines the interface for querying the audit module.
type Module interface {
	GetParams(ctx context.Context) (*types.QueryParamsResponse, error)
	GetEpochAnchor(ctx context.Context, epochID uint64) (*types.QueryEpochAnchorResponse, error)
	GetCurrentEpochAnchor(ctx context.Context) (*types.QueryCurrentEpochAnchorResponse, error)
	GetCurrentEpoch(ctx context.Context) (*types.QueryCurrentEpochResponse, error)
	GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*types.QueryAssignedTargetsResponse, error)
	GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*types.QueryEpochReportResponse, error)

	// LEP-6 storage-truth state queries.
	GetNodeSuspicionState(ctx context.Context, supernodeAccount string) (*types.QueryNodeSuspicionStateResponse, error)
	GetReporterReliabilityState(ctx context.Context, reporterAccount string) (*types.QueryReporterReliabilityStateResponse, error)
	GetTicketDeteriorationState(ctx context.Context, ticketID string) (*types.QueryTicketDeteriorationStateResponse, error)

	// LEP-6 heal-op queries.
	GetHealOp(ctx context.Context, healOpID uint64) (*types.QueryHealOpResponse, error)
	GetHealOpsByStatus(ctx context.Context, status types.HealOpStatus, pagination *query.PageRequest) (*types.QueryHealOpsByStatusResponse, error)
	GetHealOpsByTicket(ctx context.Context, ticketID string, pagination *query.PageRequest) (*types.QueryHealOpsByTicketResponse, error)
}

// NewModule creates a new Audit module client.
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
