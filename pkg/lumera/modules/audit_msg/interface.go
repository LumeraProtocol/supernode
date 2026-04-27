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
	SubmitEpochReport(
		ctx context.Context,
		epochID uint64,
		hostReport audittypes.HostReport,
		storageChallengeObservations []*audittypes.StorageChallengeObservation,
		storageProofResults []*audittypes.StorageProofResult,
	) (*sdktx.BroadcastTxResponse, error)

	// LEP-6 storage-truth tx surface.
	SubmitStorageRecheckEvidence(
		ctx context.Context,
		epochID uint64,
		challengedSupernodeAccount string,
		ticketID string,
		challengedResultTranscriptHash string,
		recheckTranscriptHash string,
		recheckResultClass audittypes.StorageProofResultClass,
		details string,
	) (*sdktx.BroadcastTxResponse, error)
	ClaimHealComplete(
		ctx context.Context,
		healOpID uint64,
		ticketID string,
		healManifestHash string,
		details string,
	) (*sdktx.BroadcastTxResponse, error)
	SubmitHealVerification(
		ctx context.Context,
		healOpID uint64,
		verified bool,
		verificationHash string,
		details string,
	) (*sdktx.BroadcastTxResponse, error)
}

// NewModule creates a new audit_msg module instance using default TxHelper
// settings (see tx.DefaultGas* constants).
func NewModule(
	conn *grpc.ClientConn,
	authmodule auth.Module,
	txmodule txmod.Module,
	kr keyring.Keyring,
	keyName string,
	chainID string,
) (Module, error) {
	return newModuleWithHelper(conn, authmodule, txmodule, &txmod.TxHelperConfig{
		ChainID: chainID,
		Keyring: kr,
		KeyName: keyName,
	})
}

// NewModuleWithTxHelperConfig creates a new audit_msg module instance with an
// explicit TxHelper configuration. Zero-valued fields in cfg fall back to
// package defaults.
func NewModuleWithTxHelperConfig(
	conn *grpc.ClientConn,
	authmodule auth.Module,
	txmodule txmod.Module,
	cfg *txmod.TxHelperConfig,
) (Module, error) {
	return newModuleWithHelper(conn, authmodule, txmodule, cfg)
}
