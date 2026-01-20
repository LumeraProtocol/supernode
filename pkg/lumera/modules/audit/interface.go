//go:generate go run go.uber.org/mock/mockgen -destination=audit_mock.go -package=audit -source=interface.go
package audit

import (
	"context"

	"github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"google.golang.org/grpc"
)

// Module defines the interface for interacting with the audit module.
type Module interface {
	CurrentWindow(ctx context.Context) (*types.QueryCurrentWindowResponse, error)
	AssignedTargets(ctx context.Context, supernodeAccount string, windowID uint64) (*types.QueryAssignedTargetsResponse, error)
}

// NewModule creates a new Audit module client.
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
