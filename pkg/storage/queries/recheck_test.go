package queries

import (
	"context"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecheckSubmissionDedupKeyEpochTicket(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	_, err := db.Exec(createStorageRecheckSubmissions)
	require.NoError(t, err)
	store := &SQLiteStore{db: db}
	ctx := context.Background()

	exists, err := store.HasRecheckSubmission(ctx, 7, "ticket-1")
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-a", "orig", "rh1", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
	exists, err = store.HasRecheckSubmission(ctx, 7, "ticket-1")
	require.NoError(t, err)
	require.True(t, exists)

	// Same ticket in a different epoch is intentionally a different replay key.
	exists, err = store.HasRecheckSubmission(ctx, 8, "ticket-1")
	require.NoError(t, err)
	require.False(t, exists)

	// INSERT OR IGNORE makes local retry recording idempotent and preserves the
	// first successful on-chain submission record.
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-b", "orig2", "rh2", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL))
	var target string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT target_account FROM storage_recheck_submissions WHERE epoch_id=? AND ticket_id=?`, 7, "ticket-1").Scan(&target))
	require.Equal(t, "target-a", target)
}

func TestRecheckPendingSubmittedAndFailureBudget(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.RecordPendingRecheckSubmission(ctx, 7, "ticket-7", "target", "challenged", "actual", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL))
	has, err := store.HasRecheckSubmission(ctx, 7, "ticket-7")
	require.NoError(t, err)
	require.True(t, has)
	require.NoError(t, store.MarkRecheckSubmissionSubmitted(ctx, 7, "ticket-7"))

	blocked, err := store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-7", 2)
	require.NoError(t, err)
	require.False(t, blocked)
	require.NoError(t, store.RecordRecheckAttemptFailure(ctx, 7, "ticket-7", "target", assert.AnError, time.Hour))
	blocked, err = store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-7", 2)
	require.NoError(t, err)
	require.False(t, blocked)
	require.NoError(t, store.RecordRecheckAttemptFailure(ctx, 7, "ticket-7", "target", assert.AnError, time.Hour))
	blocked, err = store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-7", 2)
	require.NoError(t, err)
	require.True(t, blocked)
}
