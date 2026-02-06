package audit

import (
	"context"

	"github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"google.golang.org/grpc"
)

// Module defines the interface for querying the audit module.
type Module interface {
	GetParams(ctx context.Context) (*types.QueryParamsResponse, error)
	GetEpochAnchor(ctx context.Context, epochID uint64) (*types.QueryEpochAnchorResponse, error)
	GetCurrentEpochAnchor(ctx context.Context) (*types.QueryCurrentEpochAnchorResponse, error)
	GetCurrentEpoch(ctx context.Context) (*types.QueryCurrentEpochResponse, error)
	GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*types.QueryAssignedTargetsResponse, error)
	GetAuditReport(ctx context.Context, epochID uint64, supernodeAccount string) (*types.QueryAuditReportResponse, error)
}

// NewModule creates a new Audit module client.
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
