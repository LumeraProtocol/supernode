//go:generate go run go.uber.org/mock/mockgen -destination=audit_msg_mock.go -package=audit_msg -source=interface.go
package audit_msg

import (
	"context"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	txmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// Module defines the interface for audit-related transactions.
type Module interface {
	SubmitEvidence(ctx context.Context, subjectAddress string, evidenceType audittypes.EvidenceType, actionID string, metadataJSON string) (*sdktx.BroadcastTxResponse, error)
	SubmitAuditReport(ctx context.Context, epochID uint64, peerObservations []*audittypes.AuditPeerObservation) (*sdktx.BroadcastTxResponse, error)
}

// NewModule creates a new audit_msg module instance.
func NewModule(
	conn *grpc.ClientConn,
	authmodule auth.Module,
	txmodule txmod.Module,
	kr keyring.Keyring,
	keyName string,
	chainID string,
) (Module, error) {
	return newModule(conn, authmodule, txmodule, kr, keyName, chainID)
}
