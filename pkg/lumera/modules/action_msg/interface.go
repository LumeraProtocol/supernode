//go:generate mockgen -destination=action_msg_mock.go -package=action_msg -source=interface.go
package action_msg

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

type Module interface {
	// FinalizeCascadeAction finalizes a CASCADE action with the given parameters
	RequesAction(ctx context.Context, actionType, metadata, price, expirationTime string) (*sdktx.BroadcastTxResponse, error)
	FinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string) (*sdktx.BroadcastTxResponse, error)
}

func NewModule(conn *grpc.ClientConn, authmod auth.Module, txmodule tx.Module, kr keyring.Keyring, keyName string, chainID string) (Module, error) {
	return newModule(conn, authmod, txmodule, kr, keyName, chainID)
}
