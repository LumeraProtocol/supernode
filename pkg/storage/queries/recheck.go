package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

type RecheckSubmissionRecord struct {
	EpochID                  uint64
	TicketID                 string
	TargetAccount            string
	ChallengedTranscriptHash string
	RecheckTranscriptHash    string
	ResultClass              audittypes.StorageProofResultClass
	SubmittedAt              int64
	Status                   string
}

// ErrLEP6RecheckAlreadyRecorded is returned by RecordPendingRecheckSubmission
// when a row already exists for (epoch_id, ticket_id, target_account). The
// caller (recheck attestor) treats this as "another tick already pre-staged
// this candidate" — same idempotency semantics as
// ErrLEP6ClaimAlreadyRecorded / ErrLEP6VerificationAlreadyRecorded.
//
// Wave 1 fix for L3: previous code used `INSERT OR IGNORE` which silently
// hid duplicates AND any real INSERT error (constraint violation, locked
// DB), then the caller submitted to chain anyway — the chain rejected and
// we deleted the row. Now duplicates are surfaced as a typed error and
// real INSERT failures propagate.
var ErrLEP6RecheckAlreadyRecorded = errors.New("lep6: recheck submission already recorded")

const createStorageRecheckSubmissions = `
CREATE TABLE IF NOT EXISTS storage_recheck_submissions (
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

const createStorageRecheckSubmissionStatusIndex = `CREATE INDEX IF NOT EXISTS idx_storage_recheck_submissions_status ON storage_recheck_submissions(status);`
const alterStorageRecheckSubmissionStatus = `ALTER TABLE storage_recheck_submissions ADD COLUMN status TEXT NOT NULL DEFAULT 'submitted';`

const createRecheckAttemptFailures = `
CREATE TABLE IF NOT EXISTS recheck_attempt_failures (
  epoch_id INTEGER NOT NULL,
  ticket_id TEXT NOT NULL,
  target_account TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 1,
  last_error TEXT,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY (epoch_id, ticket_id, target_account)
);`

const createRecheckAttemptFailuresExpiresIndex = `CREATE INDEX IF NOT EXISTS idx_recheck_attempt_failures_expires ON recheck_attempt_failures(expires_at);`

func migrateRecheckAttemptFailuresPK(ctx context.Context, db sqliteExecQuerier) error {
	pkCols, err := primaryKeyColumns(ctx, db, "recheck_attempt_failures")
	if err != nil {
		return err
	}
	hasTarget := false
	for _, c := range pkCols {
		if c == "target_account" {
			hasTarget = true
			break
		}
	}
	if hasTarget {
		return nil
	}
	if len(pkCols) == 0 {
		return fmt.Errorf("recheck_attempt_failures has no detectable primary key")
	}
	exec, ok := db.(interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fmt.Errorf("recheck_attempt_failures migration: db handle does not support BeginTx")
	}
	tx, err := exec.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recheck failure migration tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS recheck_attempt_failures_new;`); err != nil {
		return fmt.Errorf("drop stale recheck failure migration table: %w", err)
	}
	const createNew = `
CREATE TABLE recheck_attempt_failures_new (
  epoch_id INTEGER NOT NULL,
  ticket_id TEXT NOT NULL,
  target_account TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 1,
  last_error TEXT,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY (epoch_id, ticket_id, target_account)
);`
	if _, err := tx.ExecContext(ctx, createNew); err != nil {
		return fmt.Errorf("create new recheck failure table: %w", err)
	}
	const copyData = `
INSERT INTO recheck_attempt_failures_new
  (epoch_id, ticket_id, target_account, attempts, last_error, expires_at)
SELECT epoch_id, ticket_id, target_account, attempts, last_error, expires_at
FROM recheck_attempt_failures;`
	if _, err := tx.ExecContext(ctx, copyData); err != nil {
		return fmt.Errorf("copy recheck failure rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE recheck_attempt_failures;`); err != nil {
		return fmt.Errorf("drop old recheck failure table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE recheck_attempt_failures_new RENAME TO recheck_attempt_failures;`); err != nil {
		return fmt.Errorf("rename new recheck failure table: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit recheck failure migration: %w", err)
	}
	committed = true
	return nil
}

// migrateStorageRecheckSubmissionsPK migrates an old DB whose
// storage_recheck_submissions table has PK (epoch_id, ticket_id) up to the
// Wave 1 schema with PK (epoch_id, ticket_id, target_account).
//
// SQLite cannot ALTER PRIMARY KEY in place; we rebuild via the canonical
// "create _new, copy, drop, rename" pattern inside a single transaction so
// a crash mid-migration leaves the DB consistent.
//
// Idempotent: if the table is already on the new PK shape, this returns
// nil after the PRAGMA introspection check (no DDL run).
//
// Wave 1 fix for C2.
func migrateStorageRecheckSubmissionsPK(ctx context.Context, db sqliteExecQuerier) error {
	pkCols, err := primaryKeyColumns(ctx, db, "storage_recheck_submissions")
	if err != nil {
		return err
	}
	hasTarget := false
	for _, c := range pkCols {
		if c == "target_account" {
			hasTarget = true
			break
		}
	}
	if hasTarget {
		return nil // already migrated
	}
	if len(pkCols) == 0 {
		// Defensive: PRAGMA returned no PK columns. The CREATE TABLE
		// above always sets a PK so this would only happen on a bizarre
		// custom build; bail rather than silently rebuild.
		return fmt.Errorf("storage_recheck_submissions has no detectable primary key")
	}

	// Run inside a transaction so we don't end up with the new table but
	// the old data partially copied.
	exec, ok := db.(interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fmt.Errorf("storage_recheck_submissions migration: db handle does not support BeginTx")
	}
	tx, err := exec.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	const createNew = `
CREATE TABLE storage_recheck_submissions_new (
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
	if _, err := tx.ExecContext(ctx, createNew); err != nil {
		return fmt.Errorf("create new recheck table: %w", err)
	}
	const copyData = `
INSERT INTO storage_recheck_submissions_new
  (epoch_id, ticket_id, target_account, challenged_transcript_hash, recheck_transcript_hash, result_class, status, submitted_at)
SELECT
  epoch_id, ticket_id, target_account, challenged_transcript_hash, recheck_transcript_hash, result_class,
  COALESCE(status, 'submitted'), submitted_at
FROM storage_recheck_submissions;`
	if _, err := tx.ExecContext(ctx, copyData); err != nil {
		return fmt.Errorf("copy recheck rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE storage_recheck_submissions;`); err != nil {
		return fmt.Errorf("drop old recheck table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE storage_recheck_submissions_new RENAME TO storage_recheck_submissions;`); err != nil {
		return fmt.Errorf("rename new recheck table: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit recheck migration: %w", err)
	}
	committed = true
	return nil
}

// HasRecheckSubmission reports whether a row exists for the
// (epoch_id, ticket_id, target_account) tuple — Wave 1 fix for C2 (chain
// dedup is per-target, so multiple targets in one (epoch, ticket) must
// each be tracked separately).
func (s *SQLiteStore) HasRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount string) (bool, error) {
	const stmt = `SELECT 1 FROM storage_recheck_submissions WHERE epoch_id = ? AND ticket_id = ? AND target_account = ? LIMIT 1`
	var one int
	err := s.db.QueryRowContext(ctx, stmt, epochID, ticketID, targetAccount).Scan(&one)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecordPendingRecheckSubmission pre-stages a recheck submission row before
// chain submit. Returns ErrLEP6RecheckAlreadyRecorded when a row already
// exists for the (epoch, ticket, target) tuple — Wave 1 fix for L3 (no
// more silent INSERT-OR-IGNORE).
func (s *SQLiteStore) RecordPendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error {
	return s.recordRecheckSubmissionWithStatus(ctx, epochID, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash, resultClass, "pending", true)
}

// RecordRecheckSubmission records a submitted recheck row directly. Used by
// tests / direct paths. Idempotent (no error on duplicate) to preserve
// pre-Wave-1 caller behaviour.
func (s *SQLiteStore) RecordRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error {
	return s.recordRecheckSubmissionWithStatus(ctx, epochID, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash, resultClass, "submitted", false)
}

func (s *SQLiteStore) recordRecheckSubmissionWithStatus(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass, status string, surfaceConflict bool) error {
	if epochID == 0 || ticketID == "" {
		return fmt.Errorf("epoch_id and ticket_id are required")
	}
	const stmt = `INSERT INTO storage_recheck_submissions
  (epoch_id, ticket_id, target_account, challenged_transcript_hash, recheck_transcript_hash, result_class, status, submitted_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(epoch_id, ticket_id, target_account) DO NOTHING`
	res, err := s.db.ExecContext(ctx, stmt, epochID, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash, int32(resultClass), status, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("insert recheck submission: %w", err)
	}
	if surfaceConflict {
		n, raErr := res.RowsAffected()
		if raErr == nil && n == 0 {
			return ErrLEP6RecheckAlreadyRecorded
		}
	}
	return nil
}

// MarkRecheckSubmissionSubmitted flips a (epoch, ticket, target) row from
// 'pending' to 'submitted'. Threading target_account is the C2 fix:
// without it, two pending rows for the same (epoch, ticket) would both
// be marked when only one was actually submitted.
func (s *SQLiteStore) MarkRecheckSubmissionSubmitted(ctx context.Context, epochID uint64, ticketID, targetAccount string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE storage_recheck_submissions SET status = 'submitted', submitted_at = ? WHERE epoch_id = ? AND ticket_id = ? AND target_account = ?`, time.Now().Unix(), epochID, ticketID, targetAccount)
	return err
}

// DeletePendingRecheckSubmission deletes a single (epoch, ticket, target)
// pending row after a hard tx failure — Wave 1 C2 fix.
func (s *SQLiteStore) DeletePendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM storage_recheck_submissions WHERE epoch_id = ? AND ticket_id = ? AND target_account = ? AND status = 'pending'`, epochID, ticketID, targetAccount)
	return err
}

func (s *SQLiteStore) RecordRecheckAttemptFailure(ctx context.Context, epochID uint64, ticketID, targetAccount string, err error, ttl time.Duration) error {
	if epochID == 0 || ticketID == "" {
		return fmt.Errorf("epoch_id and ticket_id are required")
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	expiresAt := time.Now().Add(ttl).Unix()
	const stmt = `INSERT INTO recheck_attempt_failures (epoch_id, ticket_id, target_account, attempts, last_error, expires_at)
VALUES (?, ?, ?, 1, ?, ?)
ON CONFLICT(epoch_id, ticket_id, target_account) DO UPDATE SET attempts = attempts + 1, last_error = excluded.last_error, expires_at = excluded.expires_at`
	_, execErr := s.db.ExecContext(ctx, stmt, epochID, ticketID, targetAccount, msg, expiresAt)
	return execErr
}

func (s *SQLiteStore) HasRecheckAttemptFailureBudgetExceeded(ctx context.Context, epochID uint64, ticketID, targetAccount string, maxAttempts int) (bool, error) {
	if maxAttempts <= 0 {
		return false, nil
	}
	const stmt = `SELECT attempts, expires_at FROM recheck_attempt_failures WHERE epoch_id = ? AND ticket_id = ? AND target_account = ? LIMIT 1`
	var attempts int
	var expiresAt int64
	err := s.db.QueryRowContext(ctx, stmt, epochID, ticketID, targetAccount).Scan(&attempts, &expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if expiresAt <= time.Now().Unix() {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM recheck_attempt_failures WHERE epoch_id = ? AND ticket_id = ? AND target_account = ?`, epochID, ticketID, targetAccount)
		return false, nil
	}
	return attempts >= maxAttempts, nil
}

func (s *SQLiteStore) PurgeExpiredRecheckAttemptFailures(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM recheck_attempt_failures WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
