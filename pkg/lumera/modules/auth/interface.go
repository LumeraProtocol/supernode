//go:generate go run go.uber.org/mock/mockgen -destination=auth_mock.go -package=auth -source=interface.go
package auth

import (
	"context"

	"github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

type Module interface {
	// AccountInfoByAddress gets the account info by address
	AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error)
	// AccountByAddress returns the full on-chain account (any type) for the given address.
	AccountByAddress(ctx context.Context, addr string) (types.AccountI, error)

	// Verify verifies the given bytes with given supernodeAccAddress public key and returns the error
	Verify(ctx context.Context, accAddress string, data, signature []byte) (err error)
}

// NewModule creates a new Auth module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
