//go:generate mockgen -destination=auth_mock.go -package=auth -source=interface.go
package auth

import (
	"context"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

type Module interface {
	// AccountInfoByAddress gets the account info by address
	AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error)

	// Verify verifies the given bytes with given supernodeAccAddress public key and returns the error
	Verify(ctx context.Context, accAddress string, data, signature []byte) (err error)
}

// NewModule creates a new Auth module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
