package queries

import (
	"context"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

type RecheckQueries interface {
	HasRecheckSubmission(ctx context.Context, epochID uint64, ticketID string) (bool, error)
	RecordRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error
}
