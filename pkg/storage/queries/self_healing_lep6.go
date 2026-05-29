package queries

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// LEP6HealQueries — restart-safe dedup tables for the LEP-6 self-healing
// runtime. The LEP-6 dispatcher is chain-driven (poll heal-ops, role-decide
// from HealerSupernodeAccount / VerifierSupernodeAccounts), so a process
// restart that lost in-flight sync.Map entries could otherwise re-submit a
// claim or verification the chain has already accepted. Both tables are
// keyed so every (heal_op_id) or (heal_op_id, verifier) is permitted exactly
// once.
type LEP6HealQueries interface {
	// RecordPendingHealClaim pre-stages a heal claim before chain submit.
	RecordPendingHealClaim(ctx context.Context, healOpID uint64, ticketID, manifestHash, stagingDir string) error
	// MarkHealClaimSubmitted flips a pending claim to submitted after chain ack.
	MarkHealClaimSubmitted(ctx context.Context, healOpID uint64) error
	// DeletePendingHealClaim deletes only a pending claim after hard tx failure.
	DeletePendingHealClaim(ctx context.Context, healOpID uint64) error
	// RecordHealClaim persists a submitted MsgClaimHealComplete for restart-time
	// dedup. Returns ErrLEP6ClaimAlreadyRecorded if the row already exists.
	RecordHealClaim(ctx context.Context, healOpID uint64, ticketID, manifestHash, stagingDir string) error
	// HasHealClaim reports whether a SUBMITTED claim row exists for
	// healOpID. Used by the dispatcher to skip resubmission on restart.
	// Pending rows are excluded — see HasPendingHealClaim.
	HasHealClaim(ctx context.Context, healOpID uint64) (bool, error)
	// HasPendingHealClaim reports whether a pre-staged `pending` row exists
	// for healOpID — a crash mid-submit left the row behind. Restart path
	// uses this to drive a reconcile flow via GetHealOp. Wave 2 / C5 fix.
	HasPendingHealClaim(ctx context.Context, healOpID uint64) (bool, error)
	// GetHealClaim returns the persisted claim row (or sql.ErrNoRows). The
	// finalizer reads staging_dir from this row when promoting a heal-op
	// from HEALER_REPORTED to VERIFIED → publish.
	GetHealClaim(ctx context.Context, healOpID uint64) (HealClaimRecord, error)
	// ListHealClaims returns every persisted claim — used by the finalizer
	// to enumerate staging entries on a fresh tick or after restart.
	ListHealClaims(ctx context.Context) ([]HealClaimRecord, error)
	// DeleteHealClaim removes the row after the finalizer has published or
	// discarded the staging dir.
	DeleteHealClaim(ctx context.Context, healOpID uint64) error

	// RecordPendingHealVerification pre-stages a verifier vote before chain submit.
	RecordPendingHealVerification(ctx context.Context, healOpID uint64, verifierAccount string, verified bool, verificationHash string) error
	// MarkHealVerificationSubmitted flips a pending vote to submitted after chain ack.
	MarkHealVerificationSubmitted(ctx context.Context, healOpID uint64, verifierAccount string) error
	// DeletePendingHealVerification deletes only a pending verifier row after hard tx failure.
	DeletePendingHealVerification(ctx context.Context, healOpID uint64, verifierAccount string) error
	// RecordHealVerification persists a submitted MsgSubmitHealVerification.
	RecordHealVerification(ctx context.Context, healOpID uint64, verifierAccount string, verified bool, verificationHash string) error
	// HasHealVerification reports whether a SUBMITTED row exists for the
	// (heal_op_id, verifier_account) tuple. Verifier dispatch uses this
	// to skip resubmission on restart; pending rows are excluded — see
	// HasPendingHealVerification.
	HasHealVerification(ctx context.Context, healOpID uint64, verifierAccount string) (bool, error)
	// HasPendingHealVerification reports whether a pre-staged `pending`
	// row exists for (heal_op_id, verifier_account). Wave 2 / C5 fix.
	HasPendingHealVerification(ctx context.Context, healOpID uint64, verifierAccount string) (bool, error)
}

// HealClaimRecord is the row shape for heal_claims_submitted.
type HealClaimRecord struct {
	HealOpID     uint64
	TicketID     string
	ManifestHash string
	StagingDir   string
	SubmittedAt  int64
	Status       string
}

// ErrLEP6ClaimAlreadyRecorded is returned by RecordHealClaim when the
// heal_op_id has already been persisted.
var ErrLEP6ClaimAlreadyRecorded = errors.New("lep6: heal claim already recorded")

// ErrLEP6VerificationAlreadyRecorded is returned by RecordHealVerification
// when (heal_op_id, verifier_account) is already persisted.
var ErrLEP6VerificationAlreadyRecorded = errors.New("lep6: heal verification already recorded")

const createHealClaimsSubmitted = `
CREATE TABLE IF NOT EXISTS heal_claims_submitted (
    heal_op_id    INTEGER PRIMARY KEY,
    ticket_id     TEXT NOT NULL,
    manifest_hash TEXT NOT NULL,
    staging_dir   TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'submitted',
    submitted_at  INTEGER NOT NULL
);`

const createHealClaimsStatusIndex = `CREATE INDEX IF NOT EXISTS idx_heal_claims_status ON heal_claims_submitted(status);`
const alterHealClaimsSubmittedStatus = `ALTER TABLE heal_claims_submitted ADD COLUMN status TEXT NOT NULL DEFAULT 'submitted';`

const createHealVerificationsSubmitted = `
CREATE TABLE IF NOT EXISTS heal_verifications_submitted (
    heal_op_id        INTEGER NOT NULL,
    verifier_account  TEXT NOT NULL,
    verified          INTEGER NOT NULL,
    verification_hash TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'submitted',
    submitted_at      INTEGER NOT NULL,
    PRIMARY KEY (heal_op_id, verifier_account)
);`

const createHealVerificationsStatusIndex = `CREATE INDEX IF NOT EXISTS idx_heal_verifications_status ON heal_verifications_submitted(status);`
const alterHealVerificationsSubmittedStatus = `ALTER TABLE heal_verifications_submitted ADD COLUMN status TEXT NOT NULL DEFAULT 'submitted';`

func (s *SQLiteStore) RecordPendingHealClaim(ctx context.Context, healOpID uint64, ticketID, manifestHash, stagingDir string) error {
	return s.recordHealClaimWithStatus(ctx, healOpID, ticketID, manifestHash, stagingDir, "pending")
}

// RecordHealClaim — see LEP6HealQueries.RecordHealClaim.
func (s *SQLiteStore) RecordHealClaim(ctx context.Context, healOpID uint64, ticketID, manifestHash, stagingDir string) error {
	return s.recordHealClaimWithStatus(ctx, healOpID, ticketID, manifestHash, stagingDir, "submitted")
}

func (s *SQLiteStore) recordHealClaimWithStatus(ctx context.Context, healOpID uint64, ticketID, manifestHash, stagingDir, status string) error {
	const stmt = `INSERT INTO heal_claims_submitted (heal_op_id, ticket_id, manifest_hash, staging_dir, status, submitted_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, stmt, healOpID, ticketID, manifestHash, stagingDir, status, time.Now().Unix())
	if err != nil {
		if isSQLiteUniqueViolation(err) {
			return ErrLEP6ClaimAlreadyRecorded
		}
		return err
	}
	return nil
}

func (s *SQLiteStore) MarkHealClaimSubmitted(ctx context.Context, healOpID uint64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE heal_claims_submitted SET status = 'submitted', submitted_at = ? WHERE heal_op_id = ?`, time.Now().Unix(), healOpID)
	return err
}

func (s *SQLiteStore) DeletePendingHealClaim(ctx context.Context, healOpID uint64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM heal_claims_submitted WHERE heal_op_id = ? AND status = 'pending'`, healOpID)
	return err
}

// HasHealClaim returns true only when a SUBMITTED claim row exists for
// healOpID. Pending rows from an interrupted submit are intentionally
// excluded so the dispatcher's restart path can detect them via
// HasPendingHealClaim and run the resume reconcile flow (Wave 2 / C5 fix).
//
// Before Wave 2 this returned true for any status, which caused a
// pending-row left over from a crash mid-submit to permanently block
// fresh dispatch — chain stayed SCHEDULED, finalizer never fired,
// heal-op silently expired and the supernode was penalized.
func (s *SQLiteStore) HasHealClaim(ctx context.Context, healOpID uint64) (bool, error) {
	const stmt = `SELECT 1 FROM heal_claims_submitted WHERE heal_op_id = ? AND status = 'submitted' LIMIT 1`
	var x int
	err := s.db.QueryRowContext(ctx, stmt, healOpID).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// HasPendingHealClaim reports whether a `pending` claim row exists for
// healOpID — meaning a previous tick pre-staged the row but did not yet
// confirm chain acceptance (or crashed between submit and persist).
// Restart-path callers use this to drive a reconcile via GetHealOp instead
// of either skipping the op forever or blindly resubmitting.
//
// Wave 2 / C5 fix.
func (s *SQLiteStore) HasPendingHealClaim(ctx context.Context, healOpID uint64) (bool, error) {
	const stmt = `SELECT 1 FROM heal_claims_submitted WHERE heal_op_id = ? AND status = 'pending' LIMIT 1`
	var x int
	err := s.db.QueryRowContext(ctx, stmt, healOpID).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetHealClaim — see LEP6HealQueries.GetHealClaim.
func (s *SQLiteStore) GetHealClaim(ctx context.Context, healOpID uint64) (HealClaimRecord, error) {
	const stmt = `SELECT heal_op_id, ticket_id, manifest_hash, staging_dir, submitted_at, status FROM heal_claims_submitted WHERE heal_op_id = ?`
	var r HealClaimRecord
	err := s.db.QueryRowContext(ctx, stmt, healOpID).Scan(&r.HealOpID, &r.TicketID, &r.ManifestHash, &r.StagingDir, &r.SubmittedAt, &r.Status)
	return r, err
}

// ListHealClaims — see LEP6HealQueries.ListHealClaims.
func (s *SQLiteStore) ListHealClaims(ctx context.Context) ([]HealClaimRecord, error) {
	const stmt = `SELECT heal_op_id, ticket_id, manifest_hash, staging_dir, submitted_at, status FROM heal_claims_submitted ORDER BY heal_op_id ASC`
	rows, err := s.db.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]HealClaimRecord, 0)
	for rows.Next() {
		var r HealClaimRecord
		if err := rows.Scan(&r.HealOpID, &r.TicketID, &r.ManifestHash, &r.StagingDir, &r.SubmittedAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteHealClaim — see LEP6HealQueries.DeleteHealClaim.
func (s *SQLiteStore) DeleteHealClaim(ctx context.Context, healOpID uint64) error {
	const stmt = `DELETE FROM heal_claims_submitted WHERE heal_op_id = ?`
	_, err := s.db.ExecContext(ctx, stmt, healOpID)
	return err
}

func (s *SQLiteStore) RecordPendingHealVerification(ctx context.Context, healOpID uint64, verifierAccount string, verified bool, verificationHash string) error {
	return s.recordHealVerificationWithStatus(ctx, healOpID, verifierAccount, verified, verificationHash, "pending")
}

// RecordHealVerification — see LEP6HealQueries.RecordHealVerification.
func (s *SQLiteStore) RecordHealVerification(ctx context.Context, healOpID uint64, verifierAccount string, verified bool, verificationHash string) error {
	return s.recordHealVerificationWithStatus(ctx, healOpID, verifierAccount, verified, verificationHash, "submitted")
}

func (s *SQLiteStore) recordHealVerificationWithStatus(ctx context.Context, healOpID uint64, verifierAccount string, verified bool, verificationHash, status string) error {
	const stmt = `INSERT INTO heal_verifications_submitted (heal_op_id, verifier_account, verified, verification_hash, status, submitted_at) VALUES (?, ?, ?, ?, ?, ?)`
	verifiedInt := 0
	if verified {
		verifiedInt = 1
	}
	_, err := s.db.ExecContext(ctx, stmt, healOpID, verifierAccount, verifiedInt, verificationHash, status, time.Now().Unix())
	if err != nil {
		if isSQLiteUniqueViolation(err) {
			return ErrLEP6VerificationAlreadyRecorded
		}
		return err
	}
	return nil
}

func (s *SQLiteStore) MarkHealVerificationSubmitted(ctx context.Context, healOpID uint64, verifierAccount string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE heal_verifications_submitted SET status = 'submitted', submitted_at = ? WHERE heal_op_id = ? AND verifier_account = ?`, time.Now().Unix(), healOpID, verifierAccount)
	return err
}

func (s *SQLiteStore) DeletePendingHealVerification(ctx context.Context, healOpID uint64, verifierAccount string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM heal_verifications_submitted WHERE heal_op_id = ? AND verifier_account = ? AND status = 'pending'`, healOpID, verifierAccount)
	return err
}

// HasHealVerification reports whether a SUBMITTED verifier row exists for
// (healOpID, verifierAccount). Pending rows from an interrupted submit are
// excluded — Wave 2 / C5 fix mirroring HasHealClaim.
func (s *SQLiteStore) HasHealVerification(ctx context.Context, healOpID uint64, verifierAccount string) (bool, error) {
	const stmt = `SELECT 1 FROM heal_verifications_submitted WHERE heal_op_id = ? AND verifier_account = ? AND status = 'submitted' LIMIT 1`
	var x int
	err := s.db.QueryRowContext(ctx, stmt, healOpID, verifierAccount).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// HasPendingHealVerification reports whether a `pending` verifier row
// exists for (healOpID, verifierAccount) — the verifier counterpart to
// HasPendingHealClaim. Wave 2 / C5 fix.
func (s *SQLiteStore) HasPendingHealVerification(ctx context.Context, healOpID uint64, verifierAccount string) (bool, error) {
	const stmt = `SELECT 1 FROM heal_verifications_submitted WHERE heal_op_id = ? AND verifier_account = ? AND status = 'pending' LIMIT 1`
	var x int
	err := s.db.QueryRowContext(ctx, stmt, healOpID, verifierAccount).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// isSQLiteUniqueViolation matches both the sqlite3 driver's typed error and
// the textual surface ("UNIQUE constraint failed") so the dedup helpers stay
// portable against driver changes.
func isSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY must be unique")
}
