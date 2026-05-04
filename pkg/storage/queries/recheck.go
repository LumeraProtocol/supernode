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
}

const createStorageRecheckSubmissions = `
CREATE TABLE IF NOT EXISTS storage_recheck_submissions (
  epoch_id INTEGER NOT NULL,
  ticket_id TEXT NOT NULL,
  target_account TEXT NOT NULL,
  challenged_transcript_hash TEXT NOT NULL,
  recheck_transcript_hash TEXT NOT NULL,
  result_class INTEGER NOT NULL,
  submitted_at INTEGER NOT NULL,
  PRIMARY KEY (epoch_id, ticket_id)
);`

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

func (s *SQLiteStore) RecordRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error {
	const stmt = `INSERT OR IGNORE INTO storage_recheck_submissions (epoch_id, ticket_id, target_account, challenged_transcript_hash, recheck_transcript_hash, result_class, submitted_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if epochID == 0 || ticketID == "" {
		return fmt.Errorf("epoch_id and ticket_id are required")
	}
	_, err := s.db.ExecContext(ctx, stmt, epochID, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash, int32(resultClass), time.Now().Unix())
	return err
}
