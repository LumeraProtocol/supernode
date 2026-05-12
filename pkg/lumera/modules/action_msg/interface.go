//go:generate go run go.uber.org/mock/mockgen -destination=action_msg_mock.go -package=action_msg -source=interface.go
package action_msg

import (
	"context"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

type Module interface {
	// RequestAction submits a new action request
	RequestAction(ctx context.Context, actionType, metadata, price, expirationTime, fileSizeKbs string) (*sdktx.BroadcastTxResponse, error)
	// FinalizeCascadeAction finalizes a CASCADE action with rqIDs and optional LEP-5 chunk proofs
	FinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string, chunkProofs []*actiontypes.ChunkProof) (*sdktx.BroadcastTxResponse, error)
	// SimulateFinalizeCascadeAction simulates the finalize action (no broadcast)
	SimulateFinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string, chunkProofs []*actiontypes.ChunkProof) (*sdktx.SimulateResponse, error)
}

// NewModule creates an action_msg module using default TxHelper settings.
// Preserved for backward compatibility. For customized gas policy use NewModuleWithTxHelperConfig.
func NewModule(conn *grpc.ClientConn, authmod auth.Module, txmodule tx.Module, kr keyring.Keyring, keyName string, chainID string) (Module, error) {
	return newModuleWithHelper(conn, authmod, txmodule, &tx.TxHelperConfig{
		ChainID: chainID,
		Keyring: kr,
		KeyName: keyName,
	})
}

// NewModuleWithTxHelperConfig creates an action_msg module with an explicit TxHelper configuration.
// Zero-valued fields in cfg fall back to package defaults (see tx.DefaultGas* constants).
// cfg.ChainID / cfg.Keyring / cfg.KeyName are required (validated inside newModuleWithHelper).
func NewModuleWithTxHelperConfig(conn *grpc.ClientConn, authmod auth.Module, txmodule tx.Module, cfg *tx.TxHelperConfig) (Module, error) {
	return newModuleWithHelper(conn, authmod, txmodule, cfg)
}
