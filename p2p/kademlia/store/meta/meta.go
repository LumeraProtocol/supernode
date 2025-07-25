package meta

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/LumeraProtocol/supernode/p2p/kademlia/domain"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"

	"database/sql"

	"github.com/cenkalti/backoff/v4"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" //go-sqlite3
)

// Exponential backoff parameters
var (
	checkpointInterval = 5 * time.Second // Checkpoint interval in seconds
	dbName             = "data001-meta.sqlite3"
)

// Worker represents the worker that executes the job
type Worker struct {
	JobQueue chan Job
	quit     chan bool
}

// Store is the main struct
type Store struct {
	db     *sqlx.DB
	worker *Worker
}

// DisabledKey represents the disabled key
type DisabledKey struct {
	Key       string    `db:"key"`
	CreatedAt time.Time `db:"createdAt"`
}

// toDomain converts DisabledKey to domain.DisabledKey
func (r *DisabledKey) toDomain() (domain.DisabledKey, error) {
	return domain.DisabledKey{
		Key:       r.Key,
		CreatedAt: r.CreatedAt,
	}, nil
}

// Job represents the job to be run
type Job struct {
	JobType string // Insert, Delete
	Key     []byte

	TaskID string
	ReqID  string
}

// NewStore returns a new store
func NewStore(ctx context.Context, dataDir string) (*Store, error) {
	worker := &Worker{
		JobQueue: make(chan Job, 500),
		quit:     make(chan bool),
	}

	logtrace.Info(ctx, fmt.Sprintf("p2p data dir: %v", dataDir), logtrace.Fields{logtrace.FieldModule: "p2p"})
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0750); err != nil {
			return nil, fmt.Errorf("mkdir %q: %w", dataDir, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("cannot create data folder: %w", err)
	}

	dbFile := path.Join(dataDir, dbName)
	db, err := sqlx.Connect("sqlite3", dbFile)
	if err != nil {
		return nil, fmt.Errorf("cannot open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(10) // set appropriate value
	db.SetMaxIdleConns(5)  // set appropriate value
	db.SetConnMaxIdleTime(5 * time.Minute)

	s := &Store{
		db:     db,
		worker: worker,
	}

	if !s.checkStore() {
		if err = s.migrate(); err != nil {
			return nil, fmt.Errorf("cannot create table(s) in sqlite database: %w", err)
		}
	}

	if err := s.migrateToDelKeys(); err != nil {
		logtrace.Error(ctx, fmt.Sprintf("cannot create to-del-keys table in sqlite database: %s", err.Error()), logtrace.Fields{logtrace.FieldModule: "p2p"})
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA cache_size=-120000;",
		"PRAGMA busy_timeout=120000;",
		"PRAGMA journal_size_limit=5242880;",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("cannot set sqlite database parameter: %w", err)
		}
	}

	s.db = db

	go s.start(ctx)
	// Run WAL checkpoint worker every 5 seconds
	go s.startCheckpointWorker(ctx)

	return s, nil
}

func (s *Store) checkStore() bool {
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name='disabled_keys'`
	var name string
	err := s.db.Get(&name, query)
	return err == nil
}

func (s *Store) migrate() error {
	query := `
    CREATE TABLE IF NOT EXISTS disabled_keys(
        key TEXT PRIMARY KEY,
        createdAt DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `
	if _, err := s.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create table 'disabled_keys': %w", err)
	}

	return nil
}

func (s *Store) migrateToDelKeys() error {
	query := `
    CREATE TABLE IF NOT EXISTS del_keys(
        key TEXT PRIMARY KEY,
		count INTEGER NOT NULL DEFAULT 0,
		nodes TEXT NOT NULL DEFAULT "",
        createdAt DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `
	if _, err := s.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create table 'del_keys': %w", err)
	}

	return nil
}

func (s *Store) startCheckpointWorker(ctx context.Context) {
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 1 * time.Minute
	b.InitialInterval = 100 * time.Millisecond

	for {
		err := backoff.RetryNotify(func() error {
			err := s.checkpoint()
			if err == nil {
				// If no error, delay for 5 seconds.
				time.Sleep(checkpointInterval)
			}
			return err
		}, b, func(err error, duration time.Duration) {
			logtrace.Error(ctx, "Failed to perform checkpoint, retrying...", logtrace.Fields{"duration": duration})
		})

		if err == nil {
			b.Reset()
			b.MaxElapsedTime = 1 * time.Minute
			b.InitialInterval = 100 * time.Millisecond
		}

		select {
		case <-ctx.Done():
			logtrace.Info(ctx, "Stopping checkpoint worker because of context cancel", logtrace.Fields{})
			return
		case <-s.worker.quit:
			logtrace.Info(ctx, "Stopping checkpoint worker because of quit signal", logtrace.Fields{})
			return
		default:
		}
	}
}

// Start method starts the run loop for the worker
func (s *Store) start(ctx context.Context) {
	for {
		select {
		case job := <-s.worker.JobQueue:
			if err := s.performJob(job); err != nil {
				logtrace.Error(ctx, "Failed to perform job", logtrace.Fields{logtrace.FieldError: err})
			}
		case <-s.worker.quit:
			logtrace.Info(ctx, "exit sqlite meta db worker - quit signal received", logtrace.Fields{})
			return
		case <-ctx.Done():
			logtrace.Info(ctx, "exit sqlite meta db worker- ctx done signal received", logtrace.Fields{})
			return
		}
	}
}

// Stop signals the worker to stop listening for work requests.
func (w *Worker) Stop() {
	go func() {
		w.quit <- true
	}()
}

// Store function creates a new job and pushes it into the JobQueue
func (s *Store) Store(ctx context.Context, key []byte) error {
	job := Job{
		JobType: "Insert",
		Key:     key,
	}

	if val := ctx.Value(logtrace.CorrelationIDKey); val != nil {
		switch val := val.(type) {
		case string:
			job.TaskID = val
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.worker.JobQueue <- job:
	}

	return nil
}

// PerformJob performs the job in the JobQueue
func (s *Store) performJob(j Job) error {
	switch j.JobType {
	case "Insert":
		err := s.storeDisabledKey(j.Key)
		if err != nil {
			logtrace.Error(context.Background(), "failed to store disable key record", logtrace.Fields{logtrace.FieldError: err, logtrace.FieldTaskID: j.TaskID, "id": j.ReqID})
			return fmt.Errorf("failed to store disable key record: %w", err)
		}
	case "Delete":
		s.deleteRecord(j.Key)
	}

	return nil
}

// Checkpoint method for the store
func (s *Store) checkpoint() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint;")
	if err != nil {
		return fmt.Errorf("failed to checkpoint: %w", err)
	}
	return nil
}

// Close the store
func (s *Store) Close(ctx context.Context) {
	s.worker.Stop()

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			logtrace.Error(ctx, fmt.Sprintf("Failed to close database: %s", err), logtrace.Fields{logtrace.FieldModule: "p2p"})
		}
	}
}

// Retrieve will return the queries key/value if it exists
func (s *Store) Retrieve(_ context.Context, key string) error {
	r := DisabledKey{}
	err := s.db.Get(&r, `SELECT * FROM disabled_keys WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("failed to get disabled record by key %s: %w", key, err)
	}

	if len(r.Key) == 0 {
		return fmt.Errorf("failed to get disabled record by key %s", key)
	}

	return nil
}

// Delete a key/value pair from the store
func (s *Store) Delete(_ context.Context, key []byte) {
	job := Job{
		JobType: "Delete",
		Key:     key,
	}

	s.worker.JobQueue <- job
}

// deleteRecord a key/value pair from the Store
func (s *Store) deleteRecord(key []byte) {
	hkey := hex.EncodeToString(key)

	res, err := s.db.Exec("DELETE FROM disabled_keys WHERE key = ?", hkey)
	if err != nil {
		logtrace.Debug(context.Background(), fmt.Sprintf("cannot delete disabled keys record by key %s: %v", hkey, err), logtrace.Fields{logtrace.FieldModule: "p2p"})
	}

	if rowsAffected, err := res.RowsAffected(); err != nil {
		logtrace.Debug(context.Background(), fmt.Sprintf("failed to delete disabled key record by key %s: %v", hkey, err), logtrace.Fields{logtrace.FieldModule: "p2p"})
	} else if rowsAffected == 0 {
		logtrace.Debug(context.Background(), fmt.Sprintf("failed to delete disabled key record by key %s", hkey), logtrace.Fields{logtrace.FieldModule: "p2p"})
	}
}

func (s *Store) storeDisabledKey(key []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("key cannot be empty")
	}

	operation := func() error {
		hkey := hex.EncodeToString(key)

		now := time.Now().UTC()
		r := DisabledKey{Key: hkey, CreatedAt: now}
		res, err := s.db.NamedExec(`INSERT INTO disabled_keys(key, createdAt) values(:key, :createdAt) ON CONFLICT(key) DO UPDATE SET createdAt=:createdAt`, r)
		if err != nil {
			return fmt.Errorf("cannot insert or update disabled key record with key %s: %w", hkey, err)
		}

		if rowsAffected, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("failed to insert/update disabled key record with key %s: %w", hkey, err)
		} else if rowsAffected == 0 {
			return fmt.Errorf("failed to insert/update disabled key record with key %s", hkey)
		}

		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 10 * time.Second

	err := backoff.Retry(operation, b)
	if err != nil {
		return fmt.Errorf("error storing data: %w", err)
	}

	return nil
}

// GetDisabledKeys returns all disabled keys
func (s *Store) GetDisabledKeys(from time.Time) (retKeys domain.DisabledKeys, err error) {
	var keys []DisabledKey
	if err := s.db.Select(&keys, "SELECT * FROM disabled_keys where createdAt > ?", from); err != nil {
		return nil, fmt.Errorf("error reading disabled keys from database: %w", err)
	}

	retKeys = make(domain.DisabledKeys, 0, len(keys))

	for _, key := range keys {
		domainKey, err := key.toDomain()
		if err != nil {
			return nil, fmt.Errorf("error converting disabled key to domain: %w", err)
		}
		retKeys = append(retKeys, domainKey)
	}

	return retKeys, nil
}

// DelKey represents the todel  key
type DelKey struct {
	Key       string    `db:"key"`
	CreatedAt time.Time `db:"createdAt"`
	Nodes     string    `db:"nodes"`
	Count     int       `db:"count"`
}

// toDomain converts DelKey to domain.DelKey
func (r *DelKey) toDomain() (domain.DelKey, error) {
	return domain.DelKey{
		Key:       r.Key,
		CreatedAt: r.CreatedAt,
		Nodes:     r.Nodes,
		Count:     r.Count,
	}, nil
}

// fromDomain converts a domain.DelKey to a DelKey
func fromDomain(dk domain.DelKey) DelKey {
	return DelKey{
		Key:       dk.Key,
		Nodes:     dk.Nodes,
		CreatedAt: dk.CreatedAt,
		Count:     dk.Count,
	}
}

// GetAllToDelKeys returns all dormant keys
func (s *Store) GetAllToDelKeys(count int) (retKeys domain.DelKeys, err error) {
	var keys []DelKey
	if err := s.db.Select(&keys, "SELECT * FROM del_keys where count >= ?", count); err != nil {
		return nil, fmt.Errorf("error reading del keys from database: %w", err)
	}

	retKeys = make(domain.DelKeys, 0, len(keys))

	for _, key := range keys {
		domainKey, err := key.toDomain()
		if err != nil {
			return nil, fmt.Errorf("error converting disabled key to domain: %w", err)
		}
		retKeys = append(retKeys, domainKey)
	}

	return retKeys, nil
}

// BatchInsertDelKeys inserts or updates multiple DelKey records in a single transaction
func (s *Store) BatchInsertDelKeys(ctx context.Context, delKeys domain.DelKeys) error {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("error starting transaction: %w", err)
	}

	// Preparing the statement for upsert
	stmt, err := tx.PreparexContext(ctx, `
        INSERT INTO del_keys (key, createdAt, count)
        VALUES (?, ?, ?)
        ON CONFLICT(key) 
        DO UPDATE SET count = count + EXCLUDED.count
    `)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("error preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, dk := range delKeys {
		delKey := fromDomain(dk)
		_, err := stmt.ExecContext(ctx, delKey.Key, delKey.CreatedAt, delKey.Count)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error executing insert or update: %w", err)
		}
	}

	return tx.Commit()
}

func (s *Store) BatchDeleteDelKeys(ctx context.Context, delKeys domain.DelKeys) error {
	// Start a transaction
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("error starting transaction: %w", err)
	}

	// Prepare the delete statement within the transaction context
	deleteStmt, err := tx.PreparexContext(ctx, `DELETE FROM del_keys WHERE key = ?`)
	if err != nil {
		tx.Rollback() // make sure to rollback the transaction on error
		return fmt.Errorf("error preparing delete statement: %w", err)
	}
	defer deleteStmt.Close()

	// Iterate through the provided delKeys
	for _, dk := range delKeys {
		delKey := fromDomain(dk) // Assuming this function converts domain.DelKey to the appropriate structure

		// Check if the key exists in the table
		var exists bool
		err = tx.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM del_keys WHERE key = ?)`, delKey.Key)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error checking if key exists: %w", err)
		}

		// If the key exists, delete the corresponding row
		if exists {
			_, err := deleteStmt.ExecContext(ctx, delKey.Key)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("error deleting key: %w", err)
			}
		}
		// If the key doesn't exist, move on to the next key
	}

	// Commit the transaction after processing all keys
	return tx.Commit()
}
