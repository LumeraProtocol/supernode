package queries

import (
	"context"
	"database/sql"
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

func (s *SQLiteStore) HasRecheckSubmission(ctx context.Context, epochID uint64, ticketID string) (bool, error) {
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

func (s *SQLiteStore) RecordPendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error {
	return s.recordRecheckSubmissionWithStatus(ctx, epochID, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash, resultClass, "pending")
}

func (s *SQLiteStore) RecordRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error {
	return s.recordRecheckSubmissionWithStatus(ctx, epochID, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash, resultClass, "submitted")
}

func (s *SQLiteStore) recordRecheckSubmissionWithStatus(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass, status string) error {
	const stmt = `INSERT OR IGNORE INTO storage_recheck_submissions (epoch_id, ticket_id, target_account, challenged_transcript_hash, recheck_transcript_hash, result_class, status, submitted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if epochID == 0 || ticketID == "" {
		return fmt.Errorf("epoch_id and ticket_id are required")
	}
	_, err := s.db.ExecContext(ctx, stmt, epochID, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash, int32(resultClass), status, time.Now().Unix())
	return err
}

func (s *SQLiteStore) MarkRecheckSubmissionSubmitted(ctx context.Context, epochID uint64, ticketID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE storage_recheck_submissions SET status = 'submitted', submitted_at = ? WHERE epoch_id = ? AND ticket_id = ?`, time.Now().Unix(), epochID, ticketID)
	return err
}

func (s *SQLiteStore) DeletePendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM storage_recheck_submissions WHERE epoch_id = ? AND ticket_id = ? AND status = 'pending'`, epochID, ticketID)
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
ON CONFLICT(epoch_id, ticket_id) DO UPDATE SET attempts = attempts + 1, last_error = excluded.last_error, expires_at = excluded.expires_at`
	_, execErr := s.db.ExecContext(ctx, stmt, epochID, ticketID, targetAccount, msg, expiresAt)
	return execErr
}

func (s *SQLiteStore) HasRecheckAttemptFailureBudgetExceeded(ctx context.Context, epochID uint64, ticketID string, maxAttempts int) (bool, error) {
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
