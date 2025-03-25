//go:generate mockgen -destination=tx_mock.go -package=tx -source=interface.go
package tx

import (
	"context"

	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// Module defines the interface for transaction-related operations
type Module interface {
	// BroadcastTx broadcasts a signed transaction
	BroadcastTx(ctx context.Context, txBytes []byte, mode sdktx.BroadcastMode) (*sdktx.BroadcastTxResponse, error)

	// SimulateTx simulates a transaction
	SimulateTx(ctx context.Context, txBytes []byte) (*sdktx.SimulateResponse, error)

	// GetTx gets transaction by hash
	GetTx(ctx context.Context, hash string) (*sdktx.GetTxResponse, error)
}

// NewModule creates a new Transaction module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
