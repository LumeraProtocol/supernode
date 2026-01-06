//go:generate go run go.uber.org/mock/mockgen -destination=supernode_msg_mock.go -package=supernode_msg -source=interface.go
package supernode_msg

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
)

// Module defines the interface for sending supernode-related transactions,
// such as reporting metrics for LEP-4 compliance.
type Module interface {
	// ReportMetrics resolves on-chain supernode info for the given identity,
	// builds a MsgReportSupernodeMetrics, and broadcasts it.
	ReportMetrics(ctx context.Context, identity string, metrics sntypes.SupernodeMetrics) (*sdktx.BroadcastTxResponse, error)
}

// NewModule constructs a supernode_msg Module using the shared auth and tx
// modules, the supernode query module, and keyring configuration.
func NewModule(
	conn *grpc.ClientConn,
	authmod auth.Module,
	txmod tx.Module,
	supernodeQuery supernode.Module,
	kr keyring.Keyring,
	keyName string,
	chainID string,
) (Module, error) {
	return newModule(conn, authmod, txmod, supernodeQuery, kr, keyName, chainID)
}
