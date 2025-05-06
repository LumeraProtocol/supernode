//go:generate mockgen -destination=action_msg_mock.go -package=action_msg -source=interface.go
package action_msg

import (
	"context"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
)

// FinalizeActionResult represents the result of a finalized action
type FinalizeActionResult struct {
	TxHash  string // Transaction hash
	Code    uint32 // Code of the transaction
	Success bool   // Whether the transaction was successful
}

// Module defines the interface for action messages operations
type Module interface {
	// FinalizeCascadeAction finalizes a CASCADE action with the given parameters
	FinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string) (*FinalizeActionResult, error)
}

// NewModule creates a new ActionMsg module client
func NewModule(conn *grpc.ClientConn, kr keyring.Keyring, keyName string, chainID string) (Module, error) {
	return newModule(conn, kr, keyName, chainID)
}
