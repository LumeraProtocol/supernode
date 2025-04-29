//go:generate mockgen -destination=tx_mock.go -package=tx -source=interface.go
package tx

import (
	"context"

	"github.com/LumeraProtocol/supernode/gen/lumera/action/types"

	"github.com/cosmos/cosmos-sdk/client"
	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
)

// Module defines the interface for interacting with the action tx module
type Module interface {
	FinalizeAction(ctx context.Context, txConfig client.TxConfig, keyRing cKeyring.Keyring, actionID, creator, actionType, metadata, rpcURL, chainID string) (*types.MsgFinalizeActionResponse, error)
}

// NewModule creates a new Action tx module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
