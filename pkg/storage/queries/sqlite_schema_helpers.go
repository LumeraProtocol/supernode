package queries

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// columnExists reports whether the SQLite table has a column with the given
// name. Used to make `ALTER TABLE … ADD COLUMN` idempotent without relying
// on swallowing errors (which previously masked real failures like locked
// DB / disk full — Wave 1 fix for M8).
func columnExists(ctx context.Context, db sqliteExecQuerier, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLiteIdent(table)))
	if err != nil {
		return false, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   sql.NullString
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate table_info(%s): %w", table, err)
	}
	return false, nil
}

// primaryKeyColumns returns the ordered set of PRIMARY KEY columns for a
// SQLite table, lower-cased. Used to detect a stale single-column PK on
// `storage_recheck_submissions` so we can migrate it to the multi-column
// PK without relying on schema text matching.
func primaryKeyColumns(ctx context.Context, db sqliteExecQuerier, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLiteIdent(table)))
	if err != nil {
		return nil, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	type pkEntry struct {
		name  string
		order int
	}
	var entries []pkEntry
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   sql.NullString
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if pk > 0 {
			entries = append(entries, pkEntry{name: strings.ToLower(name), order: pk})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate table_info(%s): %w", table, err)
	}
	// PRAGMA returns pk-ordinal in the `pk` column; sort by that ordinal.
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].order < entries[i].order {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	cols := make([]string, len(entries))
	for i, e := range entries {
		cols[i] = e.name
	}
	return cols, nil
}

// addColumnIfMissing runs `ALTER TABLE <table> ADD COLUMN …` only when the
// column is absent. Real errors (locked DB, disk full, malformed SQL) are
// propagated rather than silently swallowed — Wave 1 fix for M8.
func addColumnIfMissing(ctx context.Context, db sqliteExecQuerier, table, column, addColumnSQL string) error {
	exists, err := columnExists(ctx, db, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, addColumnSQL); err != nil {
		return fmt.Errorf("alter table %s add column %s: %w", table, column, err)
	}
	return nil
}

// sqliteExecQuerier is the minimal subset of *sql.DB / *sqlx.DB that the
// schema helpers need. Decoupled so we can reuse them inside transactions.
type sqliteExecQuerier interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// quoteSQLiteIdent returns the identifier wrapped in double-quotes with any
// embedded double-quote escaped, so `PRAGMA table_info("…")` is safe even if
// future tables use reserved words. Only the fixed table name set in this
// package flows through here, but the safety is cheap.
func quoteSQLiteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
