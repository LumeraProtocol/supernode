//go:generate mockgen -destination=supernode_mock.go -package=supernode -source=interface.go
package supernode

import (
	"context"

	"github.com/LumeraProtocol/supernode/gen/lumera/supernode/types"

	"google.golang.org/grpc"
)

// Module defines the interface for interacting with the supernode module
type Module interface {
	GetTopSuperNodesForBlock(ctx context.Context, blockHeight uint64) (*types.QueryGetTopSuperNodesForBlockResponse, error)
	GetSuperNode(ctx context.Context, address string) (*types.QueryGetSuperNodeResponse, error)
	GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*types.SuperNode, error)
}

// NewModule creates a new SuperNode module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
