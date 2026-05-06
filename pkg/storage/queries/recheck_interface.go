package queries

import (
	"context"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

type RecheckQueries interface {
	HasRecheckSubmission(ctx context.Context, epochID uint64, ticketID string) (bool, error)
	RecordPendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error
	MarkRecheckSubmissionSubmitted(ctx context.Context, epochID uint64, ticketID string) error
	DeletePendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID string) error
	RecordRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error
	RecordRecheckAttemptFailure(ctx context.Context, epochID uint64, ticketID, targetAccount string, err error, ttl time.Duration) error
	HasRecheckAttemptFailureBudgetExceeded(ctx context.Context, epochID uint64, ticketID string, maxAttempts int) (bool, error)
	PurgeExpiredRecheckAttemptFailures(ctx context.Context) error
}
