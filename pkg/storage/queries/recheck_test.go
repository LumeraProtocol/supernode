package queries

import (
	"context"
	"errors"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecheckSubmissionDedupMatchesChainPerTicket is the PR286 F3
// regression: chain replay protection in
// lumera x/audit/v1/keeper/msg_storage_truth.go:88-90 is keyed by
// (epoch, ticket, creator), NOT target. The local supernode is the
// implicit creator, so (epoch, ticket) is the right local dedup key.
//
// Two different targets within the same (epoch, ticket) must collapse to
// ONE persisted row; the second insert is silently ignored. The previous
// (epoch, ticket, target) PK was rejected by chain on every secondary
// target submit and confused local state.
func TestRecheckSubmissionDedupMatchesChainPerTicket(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	_, err := db.Exec(createStorageRecheckSubmissions)
	require.NoError(t, err)
	store := &SQLiteStore{db: db}
	ctx := context.Background()

	// Initially nothing is recorded.
	exists, err := store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-a")
	require.NoError(t, err)
	require.False(t, exists)

	// First target gets recorded. HasRecheckSubmission now returns true
	// regardless of the target argument because chain dedup is per
	// (epoch, ticket, creator).
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-a", "orig", "rh1", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
	exists, err = store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-a")
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-b")
	require.NoError(t, err, "any target on the same (epoch, ticket) must read as already recorded — chain replay key is per (epoch, ticket, creator)")
	require.True(t, exists)

	// Second target on the SAME (epoch, ticket) is silently ignored by
	// the ON CONFLICT(epoch_id, ticket_id) DO NOTHING. RecordRecheckSubmission
	// is back-compat idempotent so this returns nil.
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-b", "orig2", "rh2", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL))

	// Confirm only the first-recorded row landed.
	var n int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_recheck_submissions WHERE epoch_id=? AND ticket_id=?`, 7, "ticket-1").Scan(&n))
	require.Equal(t, 1, n, "exactly one row per (epoch, ticket) to match chain replay semantics")
	var retainedTarget, retainedRh string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT target_account, recheck_transcript_hash FROM storage_recheck_submissions WHERE epoch_id=? AND ticket_id=?`, 7, "ticket-1").Scan(&retainedTarget, &retainedRh))
	require.Equal(t, "target-a", retainedTarget, "first-recorded target wins")
	require.Equal(t, "rh1", retainedRh, "first-recorded transcript wins")

	// Same ticket in a different epoch is intentionally a different replay key.
	exists, err = store.HasRecheckSubmission(ctx, 8, "ticket-1", "target-a")
	require.NoError(t, err)
	require.False(t, exists)

	// RecordPendingRecheckSubmission on a (epoch, ticket) that already
	// has a row surfaces the typed dedup error.
	err = store.RecordPendingRecheckSubmission(ctx, 7, "ticket-1", "target-c", "orig3", "rh3", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLEP6RecheckAlreadyRecorded), "second target on same (epoch, ticket) must surface ErrLEP6RecheckAlreadyRecorded")
}

// TestRecordPendingRecheckSubmission_DuplicateReturnsTypedError covers the
// LEP-6 L3 fix: duplicate-pending writes used to be silently swallowed
// by `INSERT OR IGNORE`; they now return ErrLEP6RecheckAlreadyRecorded so
// the attestor can branch on it.
func TestRecordPendingRecheckSubmission_DuplicateReturnsTypedError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.RecordPendingRecheckSubmission(ctx, 7, "ticket-7", "target", "challenged", "actual", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL))

	// Second pre-stage of the same triple → typed dedup error.
	err := store.RecordPendingRecheckSubmission(ctx, 7, "ticket-7", "target", "challenged", "actual", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLEP6RecheckAlreadyRecorded), "want ErrLEP6RecheckAlreadyRecorded, got %v", err)

	// RecordRecheckSubmission (the historic non-typed path) stays
	// idempotent (no error on duplicate) for back-compat.
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-7", "target", "x", "y", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
}

func TestRecheckPendingSubmittedAndFailureBudget(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.RecordPendingRecheckSubmission(ctx, 7, "ticket-7", "target", "challenged", "actual", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL))
	has, err := store.HasRecheckSubmission(ctx, 7, "ticket-7", "target")
	require.NoError(t, err)
	require.True(t, has)
	require.NoError(t, store.MarkRecheckSubmissionSubmitted(ctx, 7, "ticket-7", "target"))

	blocked, err := store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-7", "target", 2)
	require.NoError(t, err)
	require.False(t, blocked)
	require.NoError(t, store.RecordRecheckAttemptFailure(ctx, 7, "ticket-7", "target", assert.AnError, time.Hour))
	blocked, err = store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-7", "target", 2)
	require.NoError(t, err)
	require.False(t, blocked)
	require.NoError(t, store.RecordRecheckAttemptFailure(ctx, 7, "ticket-7", "target", assert.AnError, time.Hour))
	blocked, err = store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-7", "target", 2)
	require.NoError(t, err)
	require.True(t, blocked)
}
