//go:generate mockgen -destination=action_mock.go -package=action -source=interface.go
package action

import (
	"context"

	"github.com/LumeraProtocol/supernode/gen/lumera/action/types"
	"google.golang.org/grpc"
)

// Module defines the interface for interacting with the action module
type Module interface {
	GetAction(ctx context.Context, actionID string) (*types.QueryGetActionResponse, error)
	GetActionFee(ctx context.Context, dataSize string) (*types.QueryGetActionFeeResponse, error)
}

// NewModule creates a new Action module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
