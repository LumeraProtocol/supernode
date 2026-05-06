package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LEP-6 review M9 (Matee, 2026-05-06): persist `lastSubmittedEpoch` to SQLite
// so that a supernode restart does not replay storage-challenge dispatch for
// the most-recently-submitted epoch. Previously this was an in-memory variable
// in `supernode/storage_challenge/service.go` (`lastRunEpoch`); after a crash
// the process would re-dispatch and re-submit the same epoch on the very next
// tick, doubling the keyring spend and burning observer/host-reporter time.
//
// Storage shape: a tiny single-row key-value table `storage_challenge_state`
// keyed by an arbitrary `state_key` string so the same table can hold any
// future per-service scalar without a schema migration. The first key we use
// is `lep6.last_submitted_epoch`. Reads return (0, false, nil) on a fresh DB.

const createStorageChallengeStateTable = `
CREATE TABLE IF NOT EXISTS storage_challenge_state (
    state_key TEXT PRIMARY KEY NOT NULL,
    epoch_id  INTEGER NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// LEP6LastSubmittedEpochKey is the storage_challenge_state row key used by
// the storage-challenge dispatcher to persist the last successfully-dispatched
// epoch. Exported so callers can build per-service variants if/when needed.
const LEP6LastSubmittedEpochKey = "lep6.last_submitted_epoch"

// StorageChallengeStateQueries persists supernode-side scalar state that must
// survive process restarts (e.g. lastSubmittedEpoch).
type StorageChallengeStateQueries interface {
	// GetStorageChallengeState returns (epoch, true, nil) if the row exists,
	// or (0, false, nil) if there is no row yet for the key. Returns a
	// non-nil error only on storage faults.
	GetStorageChallengeState(ctx context.Context, key string) (uint64, bool, error)

	// SetStorageChallengeState upserts the row to (key, epoch). Idempotent.
	SetStorageChallengeState(ctx context.Context, key string, epoch uint64) error
}

// GetStorageChallengeState — see interface comment.
func (s *SQLiteStore) GetStorageChallengeState(ctx context.Context, key string) (uint64, bool, error) {
	if s == nil || s.db == nil {
		return 0, false, errors.New("sqlite store is nil")
	}
	var epoch uint64
	row := s.db.QueryRowContext(ctx,
		`SELECT epoch_id FROM storage_challenge_state WHERE state_key = ? LIMIT 1`, key)
	if err := row.Scan(&epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("query storage_challenge_state(%q): %w", key, err)
	}
	return epoch, true, nil
}

// SetStorageChallengeState — see interface comment.
func (s *SQLiteStore) SetStorageChallengeState(ctx context.Context, key string, epoch uint64) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is nil")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO storage_challenge_state (state_key, epoch_id, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(state_key) DO UPDATE SET
		    epoch_id   = excluded.epoch_id,
		    updated_at = CURRENT_TIMESTAMP
	`, key, epoch); err != nil {
		return fmt.Errorf("upsert storage_challenge_state(%q,%d): %w", key, epoch, err)
	}
	return nil
}
