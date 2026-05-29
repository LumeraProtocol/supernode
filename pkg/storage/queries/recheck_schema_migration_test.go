package queries

import (
	"context"
	"fmt"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// TestColumnExists exercises the M8 helper directly.
func TestColumnExists(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE t1 (a INTEGER, b TEXT);`)
	require.NoError(t, err)
	ctx := context.Background()

	exists, err := columnExists(ctx, db, "t1", "a")
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = columnExists(ctx, db, "t1", "B") // case-insensitive
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = columnExists(ctx, db, "t1", "missing")
	require.NoError(t, err)
	require.False(t, exists)
}

// TestAddColumnIfMissing_Idempotent covers the M8 fix: ALTER TABLE
// ADD COLUMN runs once on fresh DBs (already has column → no-op),
// once on legacy DBs (column added), and is then idempotent on subsequent
// startups. Real ALTER errors propagate (no silent swallow).
func TestAddColumnIfMissing_Idempotent(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE t (a INTEGER);`) // legacy shape — missing 'extra'
	require.NoError(t, err)
	ctx := context.Background()

	// First call adds the column.
	require.NoError(t, addColumnIfMissing(ctx, db, "t", "extra", `ALTER TABLE t ADD COLUMN extra TEXT NOT NULL DEFAULT 'x';`))
	exists, err := columnExists(ctx, db, "t", "extra")
	require.NoError(t, err)
	require.True(t, exists)

	// Second call must be a no-op (does NOT re-issue the ALTER, which
	// would error with "duplicate column name").
	require.NoError(t, addColumnIfMissing(ctx, db, "t", "extra", `ALTER TABLE t ADD COLUMN extra TEXT NOT NULL DEFAULT 'x';`))
}

// TestMigrateStorageRecheckSubmissionsPK_CollapseToTicketKey covers the
// PR286 F3 migration: an old DB with PK (epoch_id, ticket_id, target_account)
// is migrated down to PK (epoch_id, ticket_id) so local dedup matches chain
// replay (one recheck per (epoch, ticket, creator)). Multiple target rows
// for the same (epoch, ticket) collapse to one — preferring 'submitted'
// status over 'pending', then tie-breaking by lex-smallest target_account.
func TestMigrateStorageRecheckSubmissionsPK_CollapseToTicketKey(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	ctx := context.Background()

	// Seed the OLD per-target PK schema.
	const oldSchema = `
CREATE TABLE storage_recheck_submissions (
  epoch_id INTEGER NOT NULL,
  ticket_id TEXT NOT NULL,
  target_account TEXT NOT NULL,
  challenged_transcript_hash TEXT NOT NULL,
  recheck_transcript_hash TEXT NOT NULL,
  result_class INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'submitted',
  submitted_at INTEGER NOT NULL,
  PRIMARY KEY (epoch_id, ticket_id, target_account)
);`
	_, err := db.Exec(oldSchema)
	require.NoError(t, err)
	// Three rows on the same (epoch, ticket):
	//   target-a pending  → loses to submitted
	//   target-b submitted → wins (lex-smallest among submitted)
	//   target-c submitted → loses to target-b on tie-break
	// Also one independent (epoch, ticket) with a single submitted row.
	_, err = db.Exec(`INSERT INTO storage_recheck_submissions VALUES (7, 'ticket-1', 'target-a', 'ch-a', 'rh-a', 1, 'pending',   1000);`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO storage_recheck_submissions VALUES (7, 'ticket-1', 'target-b', 'ch-b', 'rh-b', 1, 'submitted', 1100);`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO storage_recheck_submissions VALUES (7, 'ticket-1', 'target-c', 'ch-c', 'rh-c', 1, 'submitted', 1200);`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO storage_recheck_submissions VALUES (8, 'ticket-2', 'only-target', 'ch-x', 'rh-x', 1, 'submitted', 1300);`)
	require.NoError(t, err)

	// Confirm pre-migration PK shape.
	pk, err := primaryKeyColumns(ctx, db, "storage_recheck_submissions")
	require.NoError(t, err)
	require.Equal(t, []string{"epoch_id", "ticket_id", "target_account"}, pk)

	// Run migration.
	require.NoError(t, migrateStorageRecheckSubmissionsPK(ctx, db))

	// New PK is (epoch, ticket).
	pk, err = primaryKeyColumns(ctx, db, "storage_recheck_submissions")
	require.NoError(t, err)
	require.Equal(t, []string{"epoch_id", "ticket_id"}, pk)

	// Three (epoch=7, ticket-1) rows collapsed to ONE — and it MUST be the
	// 'submitted' target-b (status preference + lex tie-break).
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM storage_recheck_submissions WHERE epoch_id=7 AND ticket_id='ticket-1'`).Scan(&n))
	require.Equal(t, 1, n)
	var keptTarget, keptStatus string
	require.NoError(t, db.QueryRow(`SELECT target_account, status FROM storage_recheck_submissions WHERE epoch_id=7 AND ticket_id='ticket-1'`).Scan(&keptTarget, &keptStatus))
	require.Equal(t, "target-b", keptTarget, "submitted+lex-smallest target wins the collapse")
	require.Equal(t, "submitted", keptStatus)

	// The other (epoch, ticket) row is preserved verbatim.
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM storage_recheck_submissions WHERE epoch_id=8 AND ticket_id='ticket-2'`).Scan(&n))
	require.Equal(t, 1, n)

	// Idempotency: second run is a no-op.
	require.NoError(t, migrateStorageRecheckSubmissionsPK(ctx, db))

	// Post-migration: another target on the SAME (epoch, ticket) does NOT
	// produce a second row — matches chain replay semantics.
	store := &SQLiteStore{db: db}
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-zzz", "ch2", "rh2", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM storage_recheck_submissions WHERE epoch_id=7 AND ticket_id='ticket-1'`).Scan(&n))
	require.Equal(t, 1, n, "post-migration insert for a new target on existing (epoch, ticket) must be a no-op")
}

// TestMigrateStorageRecheckSubmissionsPK_AlreadyMigratedNoOp covers the
// idempotent fast-path where a fresh DB created via createStorageRecheckSubmissions
// already has the (epoch_id, ticket_id) PK.
func TestMigrateStorageRecheckSubmissionsPK_AlreadyMigratedNoOp(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	_, err := db.Exec(createStorageRecheckSubmissions)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, migrateStorageRecheckSubmissionsPK(ctx, db))
	pk, err := primaryKeyColumns(ctx, db, "storage_recheck_submissions")
	require.NoError(t, err)
	require.Equal(t, []string{"epoch_id", "ticket_id"}, pk)
}

// TestMigrateRecheckAttemptFailuresPK covers the PR286 F3 migration of the
// failure-budget table: PK (epoch, ticket, target) → PK (epoch, ticket).
// Multiple rows for the same (epoch, ticket) collapse with SUM(attempts)
// + MAX(expires_at) so the budget reflects the most aggressive prior
// retry pressure across targets.
func TestMigrateRecheckAttemptFailuresPK(t *testing.T) {
	ctx := context.Background()
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
CREATE TABLE recheck_attempt_failures (
  epoch_id INTEGER NOT NULL,
  ticket_id TEXT NOT NULL,
  target_account TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 1,
  last_error TEXT,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY (epoch_id, ticket_id, target_account)
);`)
	require.NoError(t, err)
	// Two failure rows on same (epoch, ticket): attempts 1 + 2 must sum to 3.
	// Use a far-future expires_at so HasRecheckAttemptFailureBudgetExceeded
	// doesn't TTL-evict on read.
	farFuture := time.Now().Add(24 * time.Hour).Unix()
	_, err = db.Exec(`INSERT INTO recheck_attempt_failures VALUES (7, 'ticket-1', 'target-a', 1, 'a', ?);`, farFuture)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO recheck_attempt_failures VALUES (7, 'ticket-1', 'target-b', 2, 'b', ?);`, farFuture)
	require.NoError(t, err)

	require.NoError(t, migrateRecheckAttemptFailuresPK(ctx, db))
	pk, err := primaryKeyColumns(ctx, db, "recheck_attempt_failures")
	require.NoError(t, err)
	require.Equal(t, []string{"epoch_id", "ticket_id"}, pk)

	store := &SQLiteStore{db: db}
	// After migration, the budget is shared per (epoch, ticket). With
	// summed attempts=3 already on the row, a maxAttempts=3 query
	// (any target) must report blocked.
	blocked, err := store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-1", "target-a", 3)
	require.NoError(t, err)
	require.True(t, blocked, "post-migration budget is per (epoch, ticket); summed attempts cross threshold")
	blocked, err = store.HasRecheckAttemptFailureBudgetExceeded(ctx, 7, "ticket-1", "target-b", 3)
	require.NoError(t, err)
	require.True(t, blocked, "budget query for a different target on the same (epoch, ticket) reads the same row")

	// Increment via a third target — the per-(epoch, ticket) row is
	// updated in-place (ON CONFLICT clause keys on (epoch, ticket)).
	require.NoError(t, store.RecordRecheckAttemptFailure(ctx, 7, "ticket-1", "target-c", fmt.Errorf("nope"), time.Hour))
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM recheck_attempt_failures WHERE epoch_id=7 AND ticket_id='ticket-1'`).Scan(&n))
	require.Equal(t, 1, n, "RecordRecheckAttemptFailure must update the single (epoch, ticket) row, not create a per-target row")
}
