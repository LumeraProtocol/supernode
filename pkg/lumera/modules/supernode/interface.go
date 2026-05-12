//go:generate go run go.uber.org/mock/mockgen -destination=supernode_mock.go -package=supernode -source=interface.go
package supernode

import (
	"context"

	"github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/cosmos/cosmos-sdk/types/query"
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
	GetTopSuperNodesForBlock(ctx context.Context, req *types.QueryGetTopSuperNodesForBlockRequest) (*types.QueryGetTopSuperNodesForBlockResponse, error)
	GetSuperNode(ctx context.Context, address string) (*types.QueryGetSuperNodeResponse, error)
	GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*types.SuperNode, error)
	GetSupernodeWithLatestAddress(ctx context.Context, address string) (*SuperNodeInfo, error)
	GetParams(ctx context.Context) (*types.QueryParamsResponse, error)
	ListSuperNodes(ctx context.Context) (*types.QueryListSuperNodesResponse, error)
	GetPoolState(ctx context.Context) (*types.QueryPoolStateResponse, error)
	GetSNEligibility(ctx context.Context, validatorAddress string) (*types.QuerySNEligibilityResponse, error)
	GetPayoutHistory(ctx context.Context, validatorAddress string, pagination *query.PageRequest) (*types.QueryPayoutHistoryResponse, error)
}

// NewModule creates a new SuperNode module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
