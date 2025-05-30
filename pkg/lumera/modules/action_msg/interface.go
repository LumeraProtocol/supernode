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

// Module defines the interface for action messages operations
type Module interface {
	// FinalizeCascadeAction finalizes a CASCADE action with the given parameters
	// This method now focuses only on message creation and delegates tx operations to tx module
	FinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string) (*sdktx.BroadcastTxResponse, error)
}

// NewModule creates a new ActionMsg module client
func NewModule(conn *grpc.ClientConn, authmod auth.Module, txmodule tx.Module, kr keyring.Keyring, keyName string, chainID string) (Module, error) {
	return newModule(conn, authmod, txmodule, kr, keyName, chainID)
}
