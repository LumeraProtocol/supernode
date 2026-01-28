//go:generate go run go.uber.org/mock/mockgen -destination=audit_msg_mock.go -package=audit_msg -source=interface.go
package audit_msg

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

// Module defines the interface for sending audit-related transactions.
type Module interface {
	SubmitAuditReport(
		ctx context.Context,
		windowID uint64,
		selfReport audittypes.AuditSelfReport,
		peerObservations []*audittypes.AuditPeerObservation,
	) (*sdktx.BroadcastTxResponse, error)
}

// NewModule constructs an audit_msg Module using the shared auth and tx modules and keyring configuration.
func NewModule(
	conn *grpc.ClientConn,
	authmod auth.Module,
	txmod tx.Module,
	kr keyring.Keyring,
	keyName string,
	chainID string,
) (Module, error) {
	return newModule(conn, authmod, txmod, kr, keyName, chainID)
}
