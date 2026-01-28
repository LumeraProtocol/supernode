package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

const createAuditWindowTable = `
CREATE TABLE IF NOT EXISTS audit_window (
  window_id INTEGER PRIMARY KEY,
  window_start_height INTEGER NOT NULL,
  k_window INTEGER NOT NULL,
  required_open_ports_json TEXT NOT NULL,
  targets_json TEXT NOT NULL,
  fetched_at_unix INTEGER NOT NULL
);`

const createAuditSubmissionTable = `
CREATE TABLE IF NOT EXISTS audit_submission (
  window_id INTEGER PRIMARY KEY,
  supernode_account TEXT NOT NULL,
  report_json TEXT NOT NULL,
  tx_hash TEXT,
  status TEXT NOT NULL,
  attempt_count INTEGER NOT NULL,
  last_error TEXT,
  updated_at_unix INTEGER NOT NULL
);`

type Store struct {
	db *sqlx.DB
}

type WindowRecord struct {
	WindowID              uint64
	WindowStartHeight     int64
	KWindow               uint32
	RequiredOpenPorts     []uint32
	TargetSupernodeAccts  []string
	FetchedAtUnix         int64
}

type SubmissionStatus string

const (
	SubmissionStatusPending   SubmissionStatus = "pending"
	SubmissionStatusSubmitted SubmissionStatus = "submitted"
	SubmissionStatusFailed    SubmissionStatus = "failed"
)

type SubmissionRecord struct {
	WindowID         uint64
	SupernodeAccount string
	ReportJSON       string
	TxHash           string
	Status           SubmissionStatus
	AttemptCount     int
	LastError        string
	UpdatedAtUnix    int64
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sqlx.Connect("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open audit sqlite database: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		fmt.Sprintf("PRAGMA cache_size=-%d;", DBCacheSizeKiB),
		fmt.Sprintf("PRAGMA busy_timeout=%d;", int64(DBBusyTimeout/time.Millisecond)),
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("cannot set sqlite database parameter: %w", err)
		}
	}

	if _, err := db.Exec(createAuditWindowTable); err != nil {
		return nil, fmt.Errorf("cannot create audit_window table: %w", err)
	}
	if _, err := db.Exec(createAuditSubmissionTable); err != nil {
		return nil, fmt.Errorf("cannot create audit_submission table: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) UpsertWindow(rec WindowRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}

	portsJSON, err := json.Marshal(rec.RequiredOpenPorts)
	if err != nil {
		return fmt.Errorf("marshal required ports: %w", err)
	}
	targetsJSON, err := json.Marshal(rec.TargetSupernodeAccts)
	if err != nil {
		return fmt.Errorf("marshal targets: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO audit_window (window_id, window_start_height, k_window, required_open_ports_json, targets_json, fetched_at_unix)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(window_id) DO UPDATE SET
		   window_start_height=excluded.window_start_height,
		   k_window=excluded.k_window,
		   required_open_ports_json=excluded.required_open_ports_json,
		   targets_json=excluded.targets_json,
		   fetched_at_unix=excluded.fetched_at_unix`,
		rec.WindowID,
		rec.WindowStartHeight,
		rec.KWindow,
		string(portsJSON),
		string(targetsJSON),
		rec.FetchedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("upsert audit_window: %w", err)
	}

	return nil
}

func (s *Store) GetSubmission(windowID uint64) (SubmissionRecord, bool, error) {
	if s == nil || s.db == nil {
		return SubmissionRecord{}, false, fmt.Errorf("store not initialized")
	}

	var row struct {
		WindowID         uint64         `db:"window_id"`
		SupernodeAccount string         `db:"supernode_account"`
		ReportJSON       string         `db:"report_json"`
		TxHash           sql.NullString `db:"tx_hash"`
		Status           string         `db:"status"`
		AttemptCount     int            `db:"attempt_count"`
		LastError        sql.NullString `db:"last_error"`
		UpdatedAtUnix    int64          `db:"updated_at_unix"`
	}

	err := s.db.Get(&row, `SELECT window_id, supernode_account, report_json, tx_hash, status, attempt_count, last_error, updated_at_unix FROM audit_submission WHERE window_id = ?`, windowID)
	if err != nil {
		if err == sql.ErrNoRows {
			return SubmissionRecord{}, false, nil
		}
		return SubmissionRecord{}, false, fmt.Errorf("query audit_submission: %w", err)
	}

	return SubmissionRecord{
		WindowID:         row.WindowID,
		SupernodeAccount: row.SupernodeAccount,
		ReportJSON:       row.ReportJSON,
		TxHash:           row.TxHash.String,
		Status:           SubmissionStatus(row.Status),
		AttemptCount:     row.AttemptCount,
		LastError:        row.LastError.String,
		UpdatedAtUnix:    row.UpdatedAtUnix,
	}, true, nil
}

func (s *Store) UpsertSubmission(rec SubmissionRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if rec.SupernodeAccount == "" {
		return fmt.Errorf("supernode_account is required")
	}
	if rec.ReportJSON == "" {
		return fmt.Errorf("report_json is required")
	}
	if rec.Status == "" {
		rec.Status = SubmissionStatusPending
	}
	if rec.UpdatedAtUnix == 0 {
		rec.UpdatedAtUnix = time.Now().Unix()
	}

	_, err := s.db.Exec(
		`INSERT INTO audit_submission (window_id, supernode_account, report_json, tx_hash, status, attempt_count, last_error, updated_at_unix)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(window_id) DO UPDATE SET
		   supernode_account=excluded.supernode_account,
		   report_json=excluded.report_json,
		   tx_hash=excluded.tx_hash,
		   status=excluded.status,
		   attempt_count=excluded.attempt_count,
		   last_error=excluded.last_error,
		   updated_at_unix=excluded.updated_at_unix`,
		rec.WindowID,
		rec.SupernodeAccount,
		rec.ReportJSON,
		nullIfEmpty(rec.TxHash),
		string(rec.Status),
		rec.AttemptCount,
		nullIfEmpty(rec.LastError),
		rec.UpdatedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("upsert audit_submission: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

