//go:generate mockgen -destination=bank_mock.go -package=bank -source=interface.go
package bank

import (
	"context"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
)

// Module provides access to Cosmos SDK bank queries.
type Module interface {
	// Balance returns the balance for a specific denom at an address.
	Balance(ctx context.Context, address string, denom string) (*banktypes.QueryBalanceResponse, error)
}

// NewModule constructs a bank Module backed by the given gRPC connection.
func NewModule(conn *grpc.ClientConn) (Module, error) { return newModule(conn) }
