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

// TestRecheckSubmissionDedupPerTarget asserts the Wave 1 / C2 fix: chain
// dedup is per-(epoch, ticket, target_account), so two distinct targets
// within the same (epoch, ticket) must produce two persisted rows. Before
// Wave 1, the PK was (epoch, ticket) and the second target's row was
// silently dropped — masking that supernode from chain N/R/D math.
func TestRecheckSubmissionDedupPerTarget(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	_, err := db.Exec(createStorageRecheckSubmissions)
	require.NoError(t, err)
	store := &SQLiteStore{db: db}
	ctx := context.Background()

	// Initially nothing is recorded for either target.
	exists, err := store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-a")
	require.NoError(t, err)
	require.False(t, exists)

	// First target gets recorded.
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-a", "orig", "rh1", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
	exists, err = store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-a")
	require.NoError(t, err)
	require.True(t, exists)

	// Second target in the SAME (epoch, ticket) must also be recorded
	// (this is the C2 fix — old behaviour silently dropped this row).
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-b", "orig2", "rh2", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL))
	exists, err = store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-b")
	require.NoError(t, err)
	require.True(t, exists)

	// Confirm both rows landed.
	var n int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_recheck_submissions WHERE epoch_id=? AND ticket_id=?`, 7, "ticket-1").Scan(&n))
	require.Equal(t, 2, n)

	// Same ticket in a different epoch is intentionally a different replay key.
	exists, err = store.HasRecheckSubmission(ctx, 8, "ticket-1", "target-a")
	require.NoError(t, err)
	require.False(t, exists)

	// Idempotent second-call on the same (epoch, ticket, target) is a no-op
	// (ON CONFLICT DO NOTHING) — preserves first row.
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-a", "orig", "rh1-DIFFERENT", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
	var rh string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT recheck_transcript_hash FROM storage_recheck_submissions WHERE epoch_id=? AND ticket_id=? AND target_account=?`, 7, "ticket-1", "target-a").Scan(&rh))
	require.Equal(t, "rh1", rh)
}

// TestRecordPendingRecheckSubmission_DuplicateReturnsTypedError covers the
// Wave 1 / L3 fix: duplicate-pending writes used to be silently swallowed
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
