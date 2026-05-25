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
// when a row already exists for (epoch_id, ticket_id). The caller (recheck
// attestor) treats this as "another tick already pre-staged this candidate"
// — same idempotency semantics as ErrLEP6ClaimAlreadyRecorded /
// ErrLEP6VerificationAlreadyRecorded.
//
// PR286 F3 fix: dedup is keyed by (epoch_id, ticket_id) to match the chain
// replay key in lumera x/audit/v1/keeper/msg_storage_truth.go:88-90
// (HasRecheckEvidence(epoch, ticket, creator)). The local supernode is the
// implicit creator, so (epoch, ticket) suffices. target_account is recorded
// as metadata of the selected candidate but does NOT participate in dedup.
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
  PRIMARY KEY (epoch_id, ticket_id)
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
  PRIMARY KEY (epoch_id, ticket_id)
);`

const createRecheckAttemptFailuresExpiresIndex = `CREATE INDEX IF NOT EXISTS idx_recheck_attempt_failures_expires ON recheck_attempt_failures(expires_at);`

// migrateRecheckAttemptFailuresPK collapses the per-target PK
// (epoch_id, ticket_id, target_account) back to (epoch_id, ticket_id) —
// PR286 F3 fix. Failure budget aligns with the chain replay key, so retries
// for any target within the same (epoch, ticket) share one budget.
//
// Collapse rule: SUM attempts across target rows and MAX expires_at so the
// resulting row reflects the most aggressive prior retry pressure. Keep
// lex-smallest target_account as metadata for observability.
//
// Idempotent: returns nil if already on the (epoch, ticket) PK.
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
	if !hasTarget {
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
  PRIMARY KEY (epoch_id, ticket_id)
);`
	if _, err := tx.ExecContext(ctx, createNew); err != nil {
		return fmt.Errorf("create new recheck failure table: %w", err)
	}
	const copyData = `
INSERT INTO recheck_attempt_failures_new
  (epoch_id, ticket_id, target_account, attempts, last_error, expires_at)
SELECT
  epoch_id,
  ticket_id,
  MIN(target_account) AS target_account,
  SUM(attempts) AS attempts,
  COALESCE(MAX(last_error), '') AS last_error,
  MAX(expires_at) AS expires_at
FROM recheck_attempt_failures
GROUP BY epoch_id, ticket_id;`
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
// storage_recheck_submissions table has PK
// (epoch_id, ticket_id, target_account) down to (epoch_id, ticket_id) —
// PR286 F3 fix. The per-target PK never matched chain replay semantics:
// chain accepts one recheck per (epoch, ticket, creator), so a second
// target row was always going to be chain-rejected.
//
// Collapse rule: per (epoch, ticket) prefer 'submitted' over 'pending'
// (so we don't roll back already-confirmed local state), then tie-break by
// lex-smallest target_account for determinism. Dropped rows correspond to
// target candidates that the chain would never have accepted anyway; the
// next finder tick will rediscover any still-eligible candidates from
// chain epoch reports.
//
// SQLite cannot ALTER PRIMARY KEY in place; we rebuild via the canonical
// "create _new, copy, drop, rename" pattern inside a single transaction
// so a crash mid-migration leaves the DB consistent.
//
// Idempotent: if the table is already on the new PK shape, this returns
// nil after the PRAGMA introspection check (no DDL run).
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
	if !hasTarget {
		return nil // already migrated to (epoch, ticket)
	}
	if len(pkCols) == 0 {
		return fmt.Errorf("storage_recheck_submissions has no detectable primary key")
	}

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

	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS storage_recheck_submissions_new;`); err != nil {
		return fmt.Errorf("drop stale recheck migration table: %w", err)
	}
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
  PRIMARY KEY (epoch_id, ticket_id)
);`
	if _, err := tx.ExecContext(ctx, createNew); err != nil {
		return fmt.Errorf("create new recheck table: %w", err)
	}
	// Deterministic collapse: ORDER BY prefers 'submitted' over 'pending'
	// (via CASE so case sensitivity / future status values are explicit),
	// tie-break by target_account ascending. INSERT OR IGNORE keeps only
	// the first row per (epoch, ticket) — which is the preferred one
	// given the ORDER BY above.
	const copyData = `
INSERT OR IGNORE INTO storage_recheck_submissions_new
  (epoch_id, ticket_id, target_account, challenged_transcript_hash, recheck_transcript_hash, result_class, status, submitted_at)
SELECT
  epoch_id, ticket_id, target_account, challenged_transcript_hash, recheck_transcript_hash, result_class,
  COALESCE(status, 'submitted') AS status, submitted_at
FROM storage_recheck_submissions
ORDER BY epoch_id, ticket_id,
  CASE COALESCE(status, 'submitted') WHEN 'submitted' THEN 0 ELSE 1 END,
  target_account;`
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
// (epoch_id, ticket_id) tuple — PR286 F3 fix. targetAccount is retained in
// the signature for API stability (and is the selected candidate's target,
// recorded as metadata at INSERT time) but does NOT participate in dedup
// because chain replay protection in lumera
// x/audit/v1/keeper/msg_storage_truth.go:88-90 is keyed by
// (epoch, ticket, creator) — the creator is implicit (this supernode),
// leaving (epoch, ticket) as the local dedup key.
func (s *SQLiteStore) HasRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount string) (bool, error) {
	_ = targetAccount // intentionally unused: chain replay key is (epoch, ticket, creator)
	const stmt = `SELECT 1 FROM storage_recheck_submissions WHERE epoch_id = ? AND ticket_id = ? LIMIT 1`
	var one int
	err := s.db.QueryRowContext(ctx, stmt, epochID, ticketID).Scan(&one)
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
// exists for (epoch, ticket) — PR286 F3 fix. targetAccount is stored as
// metadata of the selected candidate; another target for the same
// (epoch, ticket) would be rejected as already-recorded because chain
// accepts only one recheck per (epoch, ticket, creator).
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
ON CONFLICT(epoch_id, ticket_id) DO NOTHING`
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

// MarkRecheckSubmissionSubmitted flips a (epoch, ticket) row from
// 'pending' to 'submitted' — PR286 F3 fix. targetAccount is accepted for
// API stability but ignored in the WHERE clause: chain dedup is per
// (epoch, ticket, creator), so the single local row is the only one.
func (s *SQLiteStore) MarkRecheckSubmissionSubmitted(ctx context.Context, epochID uint64, ticketID, targetAccount string) error {
	_ = targetAccount
	_, err := s.db.ExecContext(ctx, `UPDATE storage_recheck_submissions SET status = 'submitted', submitted_at = ? WHERE epoch_id = ? AND ticket_id = ?`, time.Now().Unix(), epochID, ticketID)
	return err
}

// DeletePendingRecheckSubmission deletes a (epoch, ticket) pending row
// after a hard tx failure — PR286 F3 fix.
func (s *SQLiteStore) DeletePendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount string) error {
	_ = targetAccount
	_, err := s.db.ExecContext(ctx, `DELETE FROM storage_recheck_submissions WHERE epoch_id = ? AND ticket_id = ? AND status = 'pending'`, epochID, ticketID)
	return err
}

// RecordRecheckAttemptFailure records / increments the per-(epoch, ticket)
// failure counter — PR286 F3 fix. targetAccount is preserved as metadata
// of the most recent attempt but does NOT participate in the PK.
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
ON CONFLICT(epoch_id, ticket_id) DO UPDATE SET attempts = attempts + 1, last_error = excluded.last_error, expires_at = excluded.expires_at, target_account = excluded.target_account`
	_, execErr := s.db.ExecContext(ctx, stmt, epochID, ticketID, targetAccount, msg, expiresAt)
	return execErr
}

// HasRecheckAttemptFailureBudgetExceeded reads the per-(epoch, ticket)
// failure counter — PR286 F3 fix. targetAccount accepted for API stability
// but does not participate in the lookup.
func (s *SQLiteStore) HasRecheckAttemptFailureBudgetExceeded(ctx context.Context, epochID uint64, ticketID, targetAccount string, maxAttempts int) (bool, error) {
	_ = targetAccount
	if maxAttempts <= 0 {
		return false, nil
	}
	const stmt = `SELECT attempts, expires_at FROM recheck_attempt_failures WHERE epoch_id = ? AND ticket_id = ? LIMIT 1`
	var attempts int
	var expiresAt int64
	err := s.db.QueryRowContext(ctx, stmt, epochID, ticketID).Scan(&attempts, &expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if expiresAt <= time.Now().Unix() {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM recheck_attempt_failures WHERE epoch_id = ? AND ticket_id = ?`, epochID, ticketID)
		return false, nil
	}
	return attempts >= maxAttempts, nil
}

func (s *SQLiteStore) PurgeExpiredRecheckAttemptFailures(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM recheck_attempt_failures WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
