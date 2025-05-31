//go:generate mockgen -destination=tx_mock.go -package=tx -source=interface.go
package tx

import (
	"context"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

// TxConfig holds configuration for transaction operations
type TxConfig struct {
	ChainID       string
	Keyring       keyring.Keyring
	KeyName       string // Name of the key to use for signing
	GasLimit      uint64
	GasAdjustment float64
	GasPadding    uint64
	FeeDenom      string
	GasPrice      string
}

// Module defines the interface for transaction-related operations
type Module interface {

	// SimulateTransaction simulates a transaction with given messages and returns gas used
	SimulateTransaction(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount, config *TxConfig) (*sdktx.SimulateResponse, error)

	// BuildAndSignTransaction builds and signs a transaction with the given parameters
	BuildAndSignTransaction(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount, gasLimit uint64, fee string, config *TxConfig) ([]byte, error)

	// BroadcastTransaction broadcasts a signed transaction and returns the result
	BroadcastTransaction(ctx context.Context, txBytes []byte) (*sdktx.BroadcastTxResponse, error)

	// CalculateFee calculates the transaction fee based on gas usage and config
	CalculateFee(gasAmount uint64, config *TxConfig) string

	// ProcessTransaction handles the complete flow: simulate, build, sign, and broadcast
	ProcessTransaction(ctx context.Context, msgs []types.Msg, accountInfo *authtypes.BaseAccount, config *TxConfig) (*sdktx.BroadcastTxResponse, error)
}

// NewModule creates a new Transaction module client
func NewModule(conn *grpc.ClientConn) (Module, error) {
	return newModule(conn)
}
