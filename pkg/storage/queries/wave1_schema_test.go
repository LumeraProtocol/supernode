package queries

import (
	"context"
	"testing"

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

// TestMigrateStorageRecheckSubmissionsPK covers the C2 migration: an old
// DB with PK (epoch_id, ticket_id) is migrated to PK (epoch_id, ticket_id,
// target_account) preserving all data. Idempotent on already-migrated DBs.
func TestMigrateStorageRecheckSubmissionsPK(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	ctx := context.Background()

	// Seed the OLD schema (pre-Wave-1 PK).
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
  PRIMARY KEY (epoch_id, ticket_id)
);`
	_, err := db.Exec(oldSchema)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO storage_recheck_submissions VALUES (7, 'ticket-1', 'target-a', 'ch', 'rh', 1, 'submitted', 1234);`)
	require.NoError(t, err)

	// Confirm pre-migration PK shape.
	pk, err := primaryKeyColumns(ctx, db, "storage_recheck_submissions")
	require.NoError(t, err)
	require.Equal(t, []string{"epoch_id", "ticket_id"}, pk)

	// Run migration.
	require.NoError(t, migrateStorageRecheckSubmissionsPK(ctx, db))

	// Verify new PK shape and preserved data.
	pk, err = primaryKeyColumns(ctx, db, "storage_recheck_submissions")
	require.NoError(t, err)
	require.Equal(t, []string{"epoch_id", "ticket_id", "target_account"}, pk)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM storage_recheck_submissions WHERE epoch_id=7 AND ticket_id='ticket-1' AND target_account='target-a'`).Scan(&n))
	require.Equal(t, 1, n)

	// Idempotency: second run is a no-op.
	require.NoError(t, migrateStorageRecheckSubmissionsPK(ctx, db))

	// Multi-target now allowed under the new PK.
	store := &SQLiteStore{db: db}
	require.NoError(t, store.RecordRecheckSubmission(ctx, 7, "ticket-1", "target-b", "ch2", "rh2", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM storage_recheck_submissions WHERE epoch_id=7 AND ticket_id='ticket-1'`).Scan(&n))
	require.Equal(t, 2, n)
}

// TestMigrateStorageRecheckSubmissionsPK_AlreadyMigratedNoOp covers the
// idempotent fast-path where a fresh DB created via createStorageRecheckSubmissions
// already has the multi-column PK.
func TestMigrateStorageRecheckSubmissionsPK_AlreadyMigratedNoOp(t *testing.T) {
	db := sqlx.MustConnect("sqlite3", ":memory:")
	defer db.Close()
	_, err := db.Exec(createStorageRecheckSubmissions)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, migrateStorageRecheckSubmissionsPK(ctx, db))
	pk, err := primaryKeyColumns(ctx, db, "storage_recheck_submissions")
	require.NoError(t, err)
	require.Equal(t, []string{"epoch_id", "ticket_id", "target_account"}, pk)
}
