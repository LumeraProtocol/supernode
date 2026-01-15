package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/domain"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/cenkalti/backoff/v4"
	"github.com/jmoiron/sqlx"
)

type nodeReplicationInfo struct {
	LastReplicated *time.Time `db:"lastReplicatedAt"`
	UpdatedAt      time.Time  `db:"updatedAt"`
	CreatedAt      time.Time  `db:"createdAt"`
	Active         bool       `db:"is_active"`
	Adjusted       bool       `db:"is_adjusted"`
	IP             string     `db:"ip"`
	Port           uint16     `db:"port"`
	ID             string     `db:"id"`
	LastSeen       *time.Time `db:"last_seen"`
}

func (n *nodeReplicationInfo) toDomain() domain.NodeReplicationInfo {
	return domain.NodeReplicationInfo{
		LastReplicatedAt: n.LastReplicated,
		UpdatedAt:        n.UpdatedAt,
		CreatedAt:        n.CreatedAt,
		Active:           n.Active,
		IsAdjusted:       n.Adjusted,
		IP:               n.IP,
		Port:             n.Port,
		ID:               []byte(n.ID),
		LastSeen:         n.LastSeen,
	}
}

func (s *Store) migrateReplication() error {
	replicateQuery := `
    CREATE TABLE IF NOT EXISTS replication_info(
        id TEXT PRIMARY KEY,
        ip TEXT NOT NULL,
		port INTEGER NOT NULL,
        is_active BOOL DEFAULT FALSE,
		is_adjusted BOOL DEFAULT FALSE,
        lastReplicatedAt DATETIME,
		createdAt DATETIME DEFAULT CURRENT_TIMESTAMP,
        updatedAt DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `

	if _, err := s.db.Exec(replicateQuery); err != nil {
		return fmt.Errorf("failed to create table 'replication_info': %w", err)
	}

	return nil
}

func (s *Store) migrateRepKeys() error {
	replicateQuery := `
    CREATE TABLE IF NOT EXISTS replication_keys(
        key TEXT PRIMARY KEY,
		id TEXT NOT NULL,
        ip TEXT NOT NULL,
		port INTEGER NOT NULL,
		attempts INTEGER DEFAULT 0,
		updatedAt DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `

	if _, err := s.db.Exec(replicateQuery); err != nil {
		return fmt.Errorf("failed to create table 'replication_keys': %w", err)
	}

	return nil
}

func (s *Store) checkReplicateStore() bool {
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name='replication_info'`
	var name string
	err := s.db.Get(&name, query)
	return err == nil
}

func (s *Store) ensureAttempsColumn() error {
	rows, err := s.db.Query("PRAGMA table_info(replication_keys)")
	if err != nil {
		return fmt.Errorf("failed to fetch table 'replication_keys' info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, dtype string
		var dfltValue *string
		err = rows.Scan(&cid, &name, &dtype, &notnull, &dfltValue, &pk)
		if err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		if name == "attempts" {
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error during iteration: %w", err)
	}

	_, err = s.db.Exec(`ALTER TABLE replication_keys ADD COLUMN attempts INTEGER DEFAULT 0`)
	if err != nil {
		return fmt.Errorf("failed to add column 'attempts' to table 'data': %w", err)
	}

	return nil
}

func (s *Store) ensureLastSeenColumn() error {
	rows, err := s.db.Query("PRAGMA table_info(replication_info)")
	if err != nil {
		return fmt.Errorf("failed to fetch table 'replication_info' info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notnull, pk int
		var name, dtype string
		var dfltValue *string
		err = rows.Scan(&cid, &name, &dtype, &notnull, &dfltValue, &pk)
		if err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		if name == "last_seen" {
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error during iteration: %w", err)
	}

	_, err = s.db.Exec(`ALTER TABLE replication_info ADD COLUMN last_seen DATETIME DEFAULT NULL`)
	if err != nil {
		return fmt.Errorf("failed to add column 'last_seen' to table 'data': %w", err)
	}

	return nil
}

func (s *Store) checkReplicateKeysStore() bool {
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name='replication_keys'`
	var name string
	err := s.db.Get(&name, query)
	return err == nil
}

// GetAllToDoRepKeys returns all keys that need to be replicated
func (s *Store) GetAllToDoRepKeys(minAttempts, maxAttempts int) (domain.ToRepKeys, error) {
	var keys []domain.ToRepKey
	if err := s.db.Select(&keys, "SELECT * FROM replication_keys where attempts <= ? AND attempts >= ? LIMIT 10000", maxAttempts, minAttempts); err != nil {
		return nil, fmt.Errorf("error reading all keys from database: %w", err)
	}

	return domain.ToRepKeys(keys), nil
}

// DeleteRepKey will delete a key from the replication_keys table
func (s *Store) DeleteRepKey(hkey string) error {
	res, err := s.db.Exec("DELETE FROM replication_keys WHERE key = ?", hkey)
	if err != nil {
		return fmt.Errorf("cannot delete record from replication_keys table by key %s: %v", hkey, err)
	}

	if rowsAffected, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("cannot delete record from replication_keys table by key %s: %v", hkey, err)
	} else if rowsAffected == 0 {
		return fmt.Errorf("cannot delete record from replication_keys table by key %s ", hkey)
	}

	return nil
}

// StoreBatchRepKeys will store a batch of values with their Blake3 hash as the key
func (s *Store) StoreBatchRepKeys(values []string, id string, ip string, port uint16) error {
	operation := func() error {
		tx, err := s.db.Beginx()
		if err != nil {
			return fmt.Errorf("cannot begin transaction: %w", err)
		}

		// Prepare insert statement
		// Important: reset attempts on re-announce so keys don't get permanently stuck above the worker thresholds.
		stmt, err := tx.PrepareNamed(`INSERT INTO replication_keys(key, id, ip, port, updatedAt) values(:key, :id, :ip, :port, :updatedAt) ON CONFLICT(key) DO UPDATE SET id=:id,ip=:ip,port=:port,updatedAt=:updatedAt,attempts=0`)
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return fmt.Errorf("statement preparation failed, rollback failed: %v, original error: %w", rollbackErr, err)
			}
			return fmt.Errorf("cannot prepare statement: %w", err)
		}

		defer stmt.Close()

		// For each value, calculate its hash and insert into DB
		now := time.Now().UTC()
		for i := 0; i < len(values); i++ {
			r := domain.ToRepKey{Key: values[i], UpdatedAt: now, ID: id, IP: ip, Port: port}

			// Execute the insert statement
			_, err = stmt.Exec(r)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("cannot insert or update replication key record with key %s: %w", values[i], err)
			}
		}

		// Commit the transaction
		if err := tx.Commit(); err != nil {
			tx.Rollback()
			return fmt.Errorf("cannot commit transaction replication key update: %w", err)
		}

		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 1 * time.Minute

	err := backoff.Retry(operation, b)
	if err != nil {
		return fmt.Errorf("error storing rep keys record data: %w", err)
	}

	return nil
}

// GetKeysForReplication should return the keys of all data to be
// replicated across the network. Typically all data should be
// replicated every tReplicate seconds.
func (s *Store) GetKeysForReplication(ctx context.Context, from time.Time, to time.Time, maxKeys int) domain.KeysWithTimestamp {
	// Important: return a non-nil empty slice on success so callers can safely treat nil as "DB error".
	results := make([]domain.KeyWithTimestamp, 0)
	// Ordering by (createdAt, key) gives us a stable cursor when we need to include all keys for the
	// final createdAt value under a LIMIT.
	baseQuery := `SELECT key, createdAt FROM data WHERE createdAt > ? AND createdAt < ? ORDER BY createdAt ASC, key ASC`

	logtrace.Debug(ctx, "fetching keys for replication", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"from_time":          from,
		"to_time":            to,
		"max_keys":           maxKeys,
	})

	if maxKeys > 0 {
		limitedQuery := baseQuery + " LIMIT ?"
		if err := s.db.Select(&results, limitedQuery, from, to, maxKeys); err != nil {
			logtrace.Error(ctx, "failed to get records for replication", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error()})
			return nil
		}

		// If we hit the limit, include ALL keys that share the same createdAt as the last row so callers
		// can safely advance cursors by createdAt without skipping same-timestamp keys.
		if len(results) == maxKeys {
			boundAt := results[len(results)-1].CreatedAt
			boundKey := results[len(results)-1].Key

			var extra []domain.KeyWithTimestamp
			extraQuery := `SELECT key, createdAt FROM data WHERE createdAt = ? AND key > ? ORDER BY key ASC`
			if err := s.db.Select(&extra, extraQuery, boundAt, boundKey); err != nil {
				logtrace.Error(ctx, "failed to get records for replication (limit extension)", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				return nil
			}
			if len(extra) > 0 {
				results = append(results, extra...)
			}
		}
	} else {
		if err := s.db.Select(&results, baseQuery, from, to); err != nil {
			logtrace.Error(ctx, "failed to get records for replication", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error()})
			return nil
		}
	}

	if err := ctx.Err(); err != nil {
		logtrace.Error(ctx, "failed to get records for replication", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
		})
		return nil
	}

	logtrace.Debug(ctx, "Successfully fetched keys for replication", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"keys_count":         len(results),
	})

	return domain.KeysWithTimestamp(results)
}

// GetAllReplicationInfo returns all records in replication table
func (s *Store) GetAllReplicationInfo(_ context.Context) ([]domain.NodeReplicationInfo, error) {
	r := []nodeReplicationInfo{}
	err := s.db.Select(&r, `SELECT * FROM replication_info 
	ORDER BY 
		CASE WHEN lastReplicatedAt IS NULL THEN 0 ELSE 1 END,
		lastReplicatedAt
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to get replication info: %w", err)
	}

	list := make([]domain.NodeReplicationInfo, len(r))
	for i := range r {
		list[i] = r[i].toDomain()
	}

	return list, nil
}

// RecordExists checks if a record with the given nodeID exists in the database.
func (s *Store) RecordExists(nodeID string) (bool, error) {
	const query = "SELECT 1 FROM replication_info WHERE id = ? LIMIT 1"

	var exists int
	err := s.db.QueryRow(query, nodeID).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			// No matching record found
			return false, nil
		}
		// Other database error
		return false, fmt.Errorf("error checking record existence: %v", err)
	}

	return exists == 1, nil
}

// UpdateReplicationInfo updates replication info
func (s *Store) UpdateReplicationInfo(_ context.Context, rep domain.NodeReplicationInfo) error {
	if rep.IP == "" || len(rep.ID) == 0 || rep.Port == 0 {
		return fmt.Errorf("invalid replication info: %v", rep)
	}

	_, err := s.db.Exec(`UPDATE replication_info
       SET ip = ?, is_active = ?, is_adjusted = ?, lastReplicatedAt = ?, updatedAt = ?, port = ?, last_seen = ?
       WHERE id = ?`,
		rep.IP, rep.Active, rep.IsAdjusted, rep.LastReplicatedAt, rep.UpdatedAt, rep.Port, rep.LastSeen, string(rep.ID))
	if err != nil {
		return fmt.Errorf("failed to update replication info: %v", err)
	}

	return nil
}

// AddReplicationInfo adds replication info
func (s *Store) AddReplicationInfo(_ context.Context, rep domain.NodeReplicationInfo) error {
	if rep.IP == "" || len(rep.ID) == 0 || rep.Port == 0 {
		return fmt.Errorf("invalid replication info: %v", rep)
	}

	_, err := s.db.Exec(`INSERT INTO replication_info(id, ip, is_active, is_adjusted, lastReplicatedAt, updatedAt, port, last_seen) values(?, ?, ?, ?, ?, ?, ?, ?)`,
		string(rep.ID), rep.IP, rep.Active, rep.IsAdjusted, rep.LastReplicatedAt, rep.UpdatedAt, rep.Port, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("failed to insert replicate record: %v", err)
	}

	return err
}

const sqliteMaxVars = 900 // keep well below 999

func chunkStrings(in []string, n int) [][]string {
	if n <= 0 {
		n = sqliteMaxVars
	}
	out := make([][]string, 0, (len(in)+n-1)/n)
	for i := 0; i < len(in); i += n {
		j := i + n
		if j > len(in) {
			j = len(in)
		}
		out = append(out, in[i:j])
	}
	return out
}

// RetrieveBatchNotExist returns a list of keys (hex-decoded) that do not exist in the table
func (s *Store) RetrieveBatchNotExist(ctx context.Context, keys []string, batchSize int) ([]string, error) {
	keyMap := make(map[string]bool)

	// Convert the keys to map them for easier lookups
	for i := 0; i < len(keys); i++ {
		keyMap[keys[i]] = true
	}

	for _, batchKeys := range chunkStrings(keys, sqliteMaxVars) {

		placeholders := make([]string, len(batchKeys))
		args := make([]interface{}, len(batchKeys))

		for j, key := range batchKeys {
			placeholders[j] = "?"
			args[j] = key
		}

		query := fmt.Sprintf(`SELECT key FROM data WHERE key IN (%s)`, strings.Join(placeholders, ","))
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve records: %w", err)
		}

		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan key: %w", err)
			}

			// Remove existing keys from the map
			delete(keyMap, key)
		}

		if err := rows.Err(); err != nil {
			rows.Close() // ensure rows are closed in case of error
			return nil, fmt.Errorf("rows processing error: %w", err)
		}

		rows.Close() // explicitly close the rows

	}

	// Convert remaining keys in the map (which do not exist in the DB) to hex-decoded byte slices
	nonExistingKeys := make([]string, 0, len(keyMap))
	for key := range keyMap {
		nonExistingKeys = append(nonExistingKeys, key)

	}

	return nonExistingKeys, nil
}

func (s Store) RetrieveBatchValues(ctx context.Context, keys []string, getFromCloud bool) ([][]byte, int, error) {
	return retrieveBatchValues(ctx, s.db, keys, getFromCloud, s)
}

// RetrieveBatchValues returns a list of values for the given keys (hex-encoded).
func retrieveBatchValues(ctx context.Context, db *sqlx.DB, keys []string, getFromCloud bool, s Store) ([][]byte, int, error) {
	// Early return
	if len(keys) == 0 {
		return nil, 0, nil
	}

	// Helper: chunk keys into slices of at most sqliteMaxVars
	chunkStrings := func(in []string, n int) [][]string {
		if n <= 0 {
			n = sqliteMaxVars
		}
		out := make([][]string, 0, (len(in)+n-1)/n)
		for i := 0; i < len(in); i += n {
			j := i + n
			if j > len(in) {
				j = len(in)
			}
			out = append(out, in[i:j])
		}
		return out
	}

	// Map key -> index in the output slice (first occurrence wins)
	keyToIndex := make(map[string]int, len(keys))
	for i := range keys {
		if _, exists := keyToIndex[keys[i]]; !exists {
			keyToIndex[keys[i]] = i
		}
	}

	values := make([][]byte, len(keys))
	keysFound := 0
	var cloudKeys []string

	// Query in chunks
	for _, chunk := range chunkStrings(keys, sqliteMaxVars) {
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for i := range chunk {
			placeholders[i] = "?"
			args[i] = chunk[i]
		}

		query := fmt.Sprintf(`SELECT key, data, is_on_cloud FROM data WHERE key IN (%s)`, strings.Join(placeholders, ","))
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, keysFound, fmt.Errorf("failed to retrieve records: %w", err)
		}

		for rows.Next() {
			var key string
			var value []byte
			var is_on_cloud bool

			if err := rows.Scan(&key, &value, &is_on_cloud); err != nil {
				_ = rows.Close()
				return nil, keysFound, fmt.Errorf("failed to scan key and value: %w", err)
			}

			if idx, found := keyToIndex[key]; found {
				// Count only first-time fills (defensive against duplicates)
				if len(values[idx]) == 0 {
					keysFound++
				}
				values[idx] = value

				// Cloud-handling + access update (preserve original behavior)
				if s.IsCloudBackupOn() && !s.migrationStore.isSyncInProgress {
					if len(value) == 0 && is_on_cloud {
						cloudKeys = append(cloudKeys, key)
					}
					PostAccessUpdate([]string{key})
				}
			}
		}

		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, keysFound, fmt.Errorf("rows processing error: %w", err)
		}
		_ = rows.Close()
	}

	// Fetch missing-but-on-cloud keys if requested
	if len(cloudKeys) > 0 && getFromCloud {
		cloudValues, err := s.cloud.FetchBatch(cloudKeys)
		if err != nil {
			logtrace.Error(ctx, "failed to fetch from cloud", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
		}

		for key, value := range cloudValues {
			if idx, found := keyToIndex[key]; found {
				// Only count if we actually fill something that was empty
				if len(values[idx]) == 0 && len(value) > 0 {
					keysFound++
				}
				values[idx] = value
			}
		}

		// Best-effort background store for fetched-from-cloud values
		go func() {
			if len(cloudValues) == 0 {
				return
			}
			datList := make([][]byte, 0, len(cloudValues))
			for _, v := range cloudValues {
				datList = append(datList, v)
			}
			if err := s.StoreBatch(context.Background(), datList, 0, false); err != nil {
				logtrace.Error(ctx, "failed to store fetched data in local store", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
			}
		}()
	}

	return values, keysFound, nil
}

// BatchDeleteRepKeys will delete a list of keys from the replication_keys table
func (s *Store) BatchDeleteRepKeys(keys []string) error {

	totalAffected := int64(0)
	for _, chunk := range chunkStrings(keys, sqliteMaxVars) {
		ph := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for i := range chunk {
			ph[i] = "?"
			args[i] = chunk[i]
		}
		q := fmt.Sprintf("DELETE FROM replication_keys WHERE key IN (%s)", strings.Join(ph, ","))
		res, err := s.db.Exec(q, args...)
		if err != nil {
			return fmt.Errorf("batch delete: %w", err)
		}
		n, _ := res.RowsAffected()
		totalAffected += n
	}
	if totalAffected == 0 {
		return fmt.Errorf("no record deleted (batch) from replication_keys table")
	}

	return nil
}

// IncrementAttempts increments the attempts counter for the given keys.
func (s *Store) IncrementAttempts(keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// Use a transaction for consistency and to speed up the operation
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}

	query := `UPDATE replication_keys SET attempts = attempts + 1 WHERE key IN (?)`
	query, args, err := sqlx.In(query, keys)
	if err != nil {
		return err
	}

	// Rebind is necessary to match placeholders style to the SQL driver in use
	// (it converts "?" placeholders into the correct placeholder style, e.g., "$1, $2" for PostgreSQL)
	query = tx.Rebind(query)
	_, err = tx.Exec(query, args...)
	if err != nil {
		// rollback the transaction in case of error
		_ = tx.Rollback()
		return err
	}

	// commit the transaction
	return tx.Commit()
}

type repJobKind int

const (
	repJobLastSeen repJobKind = iota
	repJobIsActive
	repJobIsAdjusted
	repJobLastReplicated
)

type repJob struct {
	kind       repJobKind
	id         string
	isActive   bool
	isAdjusted bool
	t          time.Time
	ctx        context.Context
	done       chan error
}

type RepWriter struct {
	ch   chan repJob
	quit chan struct{}
}

func NewRepWriter(buf int) *RepWriter {
	return &RepWriter{
		ch:   make(chan repJob, buf),
		quit: make(chan struct{}),
	}
}

func (w *RepWriter) Stop() { close(w.quit) }

// startRepWriter runs a single serialized loop for replication_info updates.
func (s *Store) startRepWriter(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.repWriter.quit:
				return
			case j := <-s.repWriter.ch:
				var err error
				// pick a context for DB ops (prefer job ctx if non-nil)
				dbctx := j.ctx
				if dbctx == nil {
					dbctx = context.Background()
				}
				switch j.kind {
				case repJobLastSeen:
					_, err = s.db.ExecContext(dbctx,
						`UPDATE replication_info SET last_seen = ? WHERE id = ?`,
						time.Now().UTC(), j.id)

				case repJobIsActive:
					_, err = s.db.ExecContext(dbctx,
						`UPDATE replication_info SET is_active = ?, is_adjusted = ?, updatedAt = ? WHERE id = ?`,
						j.isActive, j.isAdjusted, time.Now().UTC(), j.id)

				case repJobIsAdjusted:
					_, err = s.db.ExecContext(dbctx,
						`UPDATE replication_info SET is_adjusted = ?, updatedAt = ? WHERE id = ?`,
						j.isAdjusted, time.Now().UTC(), j.id)

				case repJobLastReplicated:
					_, err = s.db.ExecContext(dbctx,
						`UPDATE replication_info SET lastReplicatedAt = ?, updatedAt = ? WHERE id = ?`,
						j.t, time.Now().UTC(), j.id)
				}
				// deliver result
				if j.done != nil {
					j.done <- err
				}
			}
		}
	}()
}

// UpdateLastSeen updates last_seen for a node (serialized via writer)
func (s *Store) UpdateLastSeen(ctx context.Context, id string) error {
	job := repJob{
		kind: repJobLastSeen,
		id:   id,
		ctx:  ctx,
		done: make(chan error, 1),
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.repWriter.ch <- job:
	}
	return <-job.done
}

// UpdateIsActive sets is_active / is_adjusted and bumps updatedAt (serialized)
func (s *Store) UpdateIsActive(ctx context.Context, id string, isActive bool, isAdjusted bool) error {
	job := repJob{
		kind:       repJobIsActive,
		id:         id,
		isActive:   isActive,
		isAdjusted: isAdjusted,
		ctx:        ctx,
		done:       make(chan error, 1),
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.repWriter.ch <- job:
	}
	return <-job.done
}

// UpdateIsAdjusted flips is_adjusted and bumps updatedAt (serialized)
func (s *Store) UpdateIsAdjusted(ctx context.Context, id string, isAdjusted bool) error {
	job := repJob{
		kind:       repJobIsAdjusted,
		id:         id,
		isAdjusted: isAdjusted,
		ctx:        ctx,
		done:       make(chan error, 1),
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.repWriter.ch <- job:
	}
	return <-job.done
}

// UpdateLastReplicated sets lastReplicatedAt and bumps updatedAt (serialized)
func (s *Store) UpdateLastReplicated(ctx context.Context, id string, t time.Time) error {
	job := repJob{
		kind: repJobLastReplicated,
		id:   id,
		t:    t,
		ctx:  ctx,
		done: make(chan error, 1),
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.repWriter.ch <- job:
	}
	return <-job.done
}
