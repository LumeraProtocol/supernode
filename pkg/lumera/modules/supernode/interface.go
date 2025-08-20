//go:generate mockgen -destination=supernode_mock.go -package=supernode -source=interface.go
package supernode

import (
	"context"

	"github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"google.golang.org/grpc"
)

// SuperNodeInfo contains processed supernode information with latest state and address
type SuperNodeInfo struct {
	SupernodeAccount string `json:"supernode_account"`
	ValidatorAddress string `json:"validator_address"`
	P2PPort          string `json:"p2p_port"`
	LatestAddress    string `json:"latest_address"`
	CurrentState     string `json:"current_state"`
}

// Module defines the interface for interacting with the supernode module
type Module interface {
	GetTopSuperNodesForBlock(ctx context.Context, blockHeight uint64) (*types.QueryGetTopSuperNodesForBlockResponse, error)
	GetSuperNode(ctx context.Context, address string) (*types.QueryGetSuperNodeResponse, error)
	GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*types.SuperNode, error)
	GetSupernodeWithLatestAddress(ctx context.Context, address string) (*SuperNodeInfo, error)
	GetParams(ctx context.Context) (*types.QueryParamsResponse, error)
}

// NewModule creates a new SuperNode module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
