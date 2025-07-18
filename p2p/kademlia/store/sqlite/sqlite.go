package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/p2p/kademlia/store/cloud.go"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/utils"

	"github.com/cenkalti/backoff/v4"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" //go-sqlite3
)

// Exponential backoff parameters
var (
	checkpointInterval = 5 * time.Second // Checkpoint interval in seconds
	//dbLock             sync.Mutex
	dbName                 = "data001.sqlite3"
	dbFilePath             = ""
	storeBatchRetryTimeout = 5 * time.Second
)

// Job represents the job to be run
type Job struct {
	JobType      string // Insert, Update or Delete
	Key          []byte
	Value        []byte
	Values       [][]byte
	ReplicatedAt time.Time
	TaskID       string
	ReqID        string
	DataType     int
	IsOriginal   bool
}

// Worker represents the worker that executes the job
type Worker struct {
	JobQueue chan Job
	quit     chan bool
}

// Store is the main struct
type Store struct {
	db             *sqlx.DB
	worker         *Worker
	cloud          cloud.Storage
	migrationStore *MigrationMetaStore
}

// Record is a data record
type Record struct {
	Key          string
	Data         []byte
	Datatype     int
	Isoriginal   bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ReplicatedAt time.Time
	IsOnCloud    bool `db:"is_on_cloud"`
}

// NewStore returns a new store
func NewStore(ctx context.Context, dataDir string, cloud cloud.Storage, mst *MigrationMetaStore) (*Store, error) {
	worker := &Worker{
		JobQueue: make(chan Job, 500),
		quit:     make(chan bool),
	}

	logtrace.Debug(ctx, "p2p data dir", logtrace.Fields{logtrace.FieldModule: "p2p", "data_dir": dataDir})
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
	db.SetMaxOpenConns(200) // set appropriate value
	db.SetMaxIdleConns(10)  // set appropriate value

	s := &Store{
		worker: worker,
		db:     db,
		cloud:  cloud,
	}

	if !s.checkStore() {
		if err = s.migrate(); err != nil {
			return nil, fmt.Errorf("cannot create table(s) in sqlite database: %w", err)
		}
	}

	if !s.checkReplicateStore() {
		if err = s.migrateReplication(); err != nil {
			return nil, fmt.Errorf("cannot create table(s) in sqlite database: %w", err)
		}
	}

	if !s.checkReplicateKeysStore() {
		if err = s.migrateRepKeys(); err != nil {
			return nil, fmt.Errorf("cannot create table(s) in sqlite database: %w", err)
		}
	}

	if err := s.ensureDatatypeColumn(); err != nil {
		logtrace.Error(ctx, "URGENT! unable to create datatype column in p2p database", logtrace.Fields{logtrace.FieldError: err.Error()})
	}

	if err := s.ensureAttempsColumn(); err != nil {
		logtrace.Error(ctx, "URGENT! unable to create attemps column in p2p database", logtrace.Fields{logtrace.FieldError: err.Error()})
	}

	if err := s.ensureIsOnCloudColumn(); err != nil {
		logtrace.Error(ctx, "URGENT! unable to create is_on_cloud column in p2p database", logtrace.Fields{logtrace.FieldError: err.Error()})
	}

	if err := s.ensureLastSeenColumn(); err != nil {
		logtrace.Error(ctx, "URGENT! unable to create datatype column in p2p database", logtrace.Fields{logtrace.FieldError: err.Error()})
	}

	logtrace.Debug(ctx, "p2p database creating index on key column", logtrace.Fields{logtrace.FieldModule: "p2p"})
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_key ON data(key);")
	if err != nil {
		logtrace.Error(ctx, "URGENT! unable to create index on key column in p2p database", logtrace.Fields{logtrace.FieldError: err.Error()})
	}
	logtrace.Debug(ctx, "p2p database created index on key column", logtrace.Fields{logtrace.FieldModule: "p2p"})

	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_createdat ON data(createdAt);")
	if err != nil {
		logtrace.Error(ctx, "URGENT! unable to create index on createdAt column in p2p database", logtrace.Fields{logtrace.FieldError: err.Error()})
	}
	logtrace.Debug(ctx, "p2p database created index on createdAt column", logtrace.Fields{logtrace.FieldModule: "p2p"})

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA cache_size=-20000;",
		"PRAGMA busy_timeout=120000;",
		"PRAGMA journal_size_limit=5242880;",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("cannot set sqlite database parameter: %w", err)
		}
	}

	s.db = db
	dbFilePath = dbFile

	go s.start(ctx)
	// Run WAL checkpoint worker every 5 seconds
	go s.startCheckpointWorker(ctx)

	if s.IsCloudBackupOn() {
		s.migrationStore = mst
	}

	return s, nil
}

func (s *Store) IsCloudBackupOn() bool {
	return s.cloud != nil
}

func (s *Store) checkStore() bool {
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name='data'`
	var name string
	err := s.db.Get(&name, query)
	return err == nil
}

func (s *Store) ensureIsOnCloudColumn() error {
	rows, err := s.db.Query("PRAGMA table_info(data)")
	if err != nil {
		return fmt.Errorf("failed to fetch table 'data' info: %w", err)
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

		if name == "is_on_cloud" {
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error during iteration: %w", err)
	}

	_, err = s.db.Exec(`ALTER TABLE data ADD COLUMN is_on_cloud BOOL DEFAULT false`)
	if err != nil {
		return fmt.Errorf("failed to add column 'is_on_cloud' to table 'data': %w", err)
	}

	return nil
}

func (s *Store) ensureDatatypeColumn() error {
	rows, err := s.db.Query("PRAGMA table_info(data)")
	if err != nil {
		return fmt.Errorf("failed to fetch table 'data' info: %w", err)
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

		if name == "datatype" {
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error during iteration: %w", err)
	}

	_, err = s.db.Exec(`ALTER TABLE data ADD COLUMN datatype INT DEFAULT 0`)
	if err != nil {
		return fmt.Errorf("failed to add column 'datatype' to table 'data': %w", err)
	}

	return nil
}

func (s *Store) migrate() error {
	query := `
    CREATE TABLE IF NOT EXISTS data(
        key TEXT PRIMARY KEY,
        data BLOB NOT NULL,
        is_original BOOL DEFAULT FALSE,
        createdAt DATETIME DEFAULT CURRENT_TIMESTAMP,
        updatedAt DATETIME DEFAULT CURRENT_TIMESTAMP,
        replicatedAt DATETIME,
        republishedAt DATETIME
    );
    `

	if _, err := s.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create table 'data': %w", err)
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
			logtrace.Error(ctx, "Failed to perform checkpoint, retrying...", logtrace.Fields{logtrace.FieldError: err.Error(), "duration": duration})
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
				logtrace.Error(ctx, "Failed to perform job", logtrace.Fields{logtrace.FieldError: err.Error()})
			}
		case <-s.worker.quit:
			logtrace.Info(ctx, "exit sqlite db worker - quit signal received", logtrace.Fields{})
			return
		case <-ctx.Done():
			logtrace.Info(ctx, "exit sqlite db worker- ctx done signal received", logtrace.Fields{})
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
func (s *Store) Store(ctx context.Context, key []byte, value []byte, datatype int, isOriginal bool) error {

	job := Job{
		JobType:    "Insert",
		Key:        key,
		Value:      value,
		DataType:   datatype,
		IsOriginal: isOriginal,
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

// StoreBatch stores a batch of key/value pairs for the queries node with the replication
func (s *Store) StoreBatch(ctx context.Context, values [][]byte, datatype int, isOriginal bool) error {
	job := Job{
		JobType:    "BatchInsert",
		Values:     values,
		DataType:   datatype,
		IsOriginal: isOriginal,
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

// Delete a key/value pair from the store
func (s *Store) Delete(_ context.Context, key []byte) {
	job := Job{
		JobType: "Delete",
		Key:     key,
	}

	s.worker.JobQueue <- job
}

// DeleteAll the records in store
func (s *Store) DeleteAll(ctx context.Context) error {
	job := Job{
		JobType: "DeleteAll",
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.worker.JobQueue <- job:
	}

	return nil
}

// UpdateKeyReplication updates the replication status of the key
func (s *Store) UpdateKeyReplication(ctx context.Context, key []byte) error {
	job := Job{
		JobType: "Update",
		Key:     key,
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.worker.JobQueue <- job:
	}

	return nil
}

// Retrieve will return the queries key/value if it exists
func (s *Store) Retrieve(_ context.Context, key []byte) ([]byte, error) {
	hkey := hex.EncodeToString(key)

	r := Record{}
	query := `SELECT data, is_on_cloud, is_original, datatype FROM data WHERE key = ?`
	err := s.db.QueryRow(query, hkey).Scan(&r.Data, &r.IsOnCloud, &r.Isoriginal, &r.Datatype)
	if err != nil {
		return nil, fmt.Errorf("failed to get record by key %s: %w", hkey, err)
	}

	if s.IsCloudBackupOn() && !s.migrationStore.isSyncInProgress {
		PostAccessUpdate([]string{hkey})
	}

	if len(r.Data) > 0 {
		return r.Data, nil
	}

	if !r.IsOnCloud {
		return nil, fmt.Errorf("failed to retrieve data from cloud: data is neither on cloud nor on local - this shouldn't happen")
	}

	if !s.IsCloudBackupOn() {
		return nil, fmt.Errorf("failed to retrieve data from cloud: data is supposed to be on cloud but backup is not enabled")
	}

	data, err := s.cloud.Fetch(hkey)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve data from cloud: %w", err)
	}

	if err := s.Store(context.Background(), key, data, r.Datatype, r.Isoriginal); err != nil {
		return nil, fmt.Errorf("failed to store data retrieved from cloud: %w", err)
	}

	return data, nil
}

// Checkpoint method for the store
func (s *Store) checkpoint() error {

	_, err := s.db.Exec("PRAGMA wal_checkpoint;")
	if err != nil {
		return fmt.Errorf("failed to checkpoint: %w", err)
	}
	return nil
}

// PerformJob performs the job in the JobQueue
func (s *Store) performJob(j Job) error {
	ctx := context.Background()
	switch j.JobType {
	case "Insert":
		err := s.storeRecord(j.Key, j.Value, j.DataType, j.IsOriginal)
		if err != nil {
			logtrace.Error(ctx, "failed to store record", logtrace.Fields{logtrace.FieldError: err.Error(), logtrace.FieldTaskID: j.TaskID, "id": j.ReqID})
			return fmt.Errorf("failed to store record: %w", err)
		}

	case "BatchInsert":
		err := s.storeBatchRecord(j.Values, j.DataType, j.IsOriginal)
		if err != nil {
			logtrace.Error(ctx, "failed to store batch records", logtrace.Fields{logtrace.FieldError: err.Error(), logtrace.FieldTaskID: j.TaskID, "id": j.ReqID})
			return fmt.Errorf("failed to store batch record: %w", err)
		}

		logtrace.Info(ctx, "successfully stored batch records", logtrace.Fields{logtrace.FieldTaskID: j.TaskID, "id": j.ReqID})
	case "Update":
		err := s.updateKeyReplication(j.Key, j.ReplicatedAt)
		if err != nil {
			return fmt.Errorf("failed to update key replication: %w", err)
		}
	case "Delete":
		s.deleteRecord(j.Key)
	case "DeleteAll":
		err := s.deleteAll()
		if err != nil {
			return fmt.Errorf("failed to delete record: %w", err)
		}
	}

	return nil
}

// storeRecord will store a key/value pair for the queries node
func (s *Store) storeRecord(key []byte, value []byte, typ int, isOriginal bool) error {

	hkey := hex.EncodeToString(key)
	operation := func() error {
		now := time.Now().UTC()
		r := Record{Key: hkey, Data: value, UpdatedAt: now, Datatype: typ, Isoriginal: isOriginal, CreatedAt: now}
		res, err := s.db.NamedExec(`INSERT INTO data(key, data, datatype, is_original, createdAt, updatedAt, is_on_cloud) values(:key, :data, :datatype, :isoriginal, :createdat, :updatedat, false) ON CONFLICT(key) DO UPDATE SET data=:data,updatedAt=:updatedat,is_on_cloud=false`, r)
		if err != nil {
			return fmt.Errorf("cannot insert or update record with key %s: %w", hkey, err)
		}

		if rowsAffected, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("failed to insert/update record with key %s: %w", hkey, err)
		} else if rowsAffected == 0 {
			return fmt.Errorf("failed to insert/update record with key %s", hkey)
		}

		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 10 * time.Second

	err := backoff.Retry(operation, b)
	if err != nil {
		return fmt.Errorf("error storing data: %w", err)
	}

	if s.IsCloudBackupOn() {
		PostKeysInsert([]UpdateMessage{{Key: hkey, LastAccessTime: time.Now(), Size: len(value)}})
	}

	return nil
}

// storeBatchRecord will store a batch of values with their Blake3 hash as the key
func (s *Store) storeBatchRecord(values [][]byte, typ int, isOriginal bool) error {
	hkeys := make([]UpdateMessage, len(values))

	operation := func() error {
		tx, err := s.db.Beginx()
		if err != nil {
			return fmt.Errorf("cannot begin transaction: %w", err)
		}

		// Prepare insert statement
		stmt, err := tx.PrepareNamed(`INSERT INTO data(key, data, datatype, is_original, createdAt, updatedAt, is_on_cloud) values(:key, :data, :datatype, :isoriginal, :createdat, :updatedat, false) ON CONFLICT(key) DO UPDATE SET data=:data,updatedAt=:updatedat,is_on_cloud=false`)
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
			// Compute the Blake3 hash
			hashed, err := utils.Blake3Hash(values[i])
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("cannot compute hash: %w", err)
			}

			hkey := hex.EncodeToString(hashed)
			hkeys[i] = UpdateMessage{Key: hkey, LastAccessTime: now, Size: len(values[i])}
			r := Record{Key: hkey, Data: values[i], CreatedAt: now, UpdatedAt: now, Datatype: typ, Isoriginal: isOriginal}

			// Execute the insert statement
			_, err = stmt.Exec(r)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("cannot insert or update record with key %s: %w", hkey, err)
			}
		}

		// Commit the transaction
		if err := tx.Commit(); err != nil {
			tx.Rollback()
			return fmt.Errorf("cannot commit transaction: %w", err)
		}

		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = storeBatchRetryTimeout

	err := backoff.Retry(operation, b)
	if err != nil {
		return fmt.Errorf("error storing data: %w", err)
	}

	if s.IsCloudBackupOn() {
		PostKeysInsert(hkeys)
	}

	return nil
}

// deleteRecord a key/value pair from the Store
func (s *Store) deleteRecord(key []byte) {
	ctx := context.Background()
	hkey := hex.EncodeToString(key)

	res, err := s.db.Exec("DELETE FROM data WHERE key = ?", hkey)
	if err != nil {
		logtrace.Debug(ctx, "cannot delete record by key", logtrace.Fields{logtrace.FieldModule: "p2p", "key": hkey, logtrace.FieldError: err.Error()})
	}

	if rowsAffected, err := res.RowsAffected(); err != nil {
		logtrace.Debug(ctx, "failed to delete record by key", logtrace.Fields{logtrace.FieldModule: "p2p", "key": hkey, logtrace.FieldError: err.Error()})
	} else if rowsAffected == 0 {
		logtrace.Debug(ctx, "failed to delete record by key", logtrace.Fields{logtrace.FieldModule: "p2p", "key": hkey})
	}
}

// updateKeyReplication updates the replication time for a key
func (s *Store) updateKeyReplication(key []byte, replicatedAt time.Time) error {
	keyStr := hex.EncodeToString(key)
	_, err := s.db.Exec(`UPDATE data SET replicatedAt = ? WHERE key = ?`, replicatedAt, keyStr)
	if err != nil {
		return fmt.Errorf("failed to update key replication: %v", err)
	}

	return err
}

func (s *Store) deleteAll() error {
	res, err := s.db.Exec("DELETE FROM data")
	if err != nil {
		return fmt.Errorf("cannot delete ALL records: %w", err)
	}

	if rowsAffected, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("failed to delete ALL records: %w", err)
	} else if rowsAffected == 0 {
		return fmt.Errorf("failed to delete ALL records")
	}

	return nil
}

// Count the records in store
func (s *Store) Count(_ context.Context) (int, error) {
	var count int
	err := s.db.Get(&count, `SELECT COUNT(*) FROM data`)
	if err != nil {
		return -1, fmt.Errorf("failed to get count of records: %w", err)
	}

	return count, nil
}

// Stats returns stats of store
func (s *Store) Stats(ctx context.Context) (map[string]interface{}, error) {
	stats := map[string]interface{}{}
	fi, err := os.Stat(dbFilePath)
	if err != nil {
		logtrace.Error(ctx, "failed to get p2p db size", logtrace.Fields{logtrace.FieldError: err.Error()})
	} else {
		stats["p2p_db_size"] = utils.BytesToMB(uint64(fi.Size()))
	}

	if count, err := s.Count(ctx); err == nil {
		stats["p2p_db_records_count"] = count
	} else {
		logtrace.Error(ctx, "failed to get p2p records count", logtrace.Fields{logtrace.FieldError: err.Error()})
	}

	return stats, nil
}

// Close the store
func (s *Store) Close(ctx context.Context) {
	s.worker.Stop()

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			logtrace.Error(ctx, "Failed to close database", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
		}
	}
}

// GetOwnCreatedAt func
func (s *Store) GetOwnCreatedAt(ctx context.Context) (time.Time, error) {
	var createdAtStr sql.NullString
	query := `SELECT MIN(createdAt) FROM data`

	err := s.db.Get(&createdAtStr, query)
	if err != nil {
		logtrace.Error(ctx, "failed to get own createdAt", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
		return time.Time{}, fmt.Errorf("failed to get own createdAt: %w", err)
	}

	createdAtString := createdAtStr.String
	if createdAtString == "" {
		return time.Now().UTC(), nil
	}

	createdAtString = strings.Split(createdAtString, "+")[0]

	created, err := time.Parse("2006-01-02 15:04:05.999999999", createdAtString)
	if err != nil {
		created, err = time.Parse(time.RFC3339Nano, createdAtString)
		if err != nil {
			created, err = time.Parse(time.RFC3339, createdAtString)
			if err != nil {
				created, err = time.Parse("2006-01-02 15:04:05", createdAtString)
				if err != nil {
					logtrace.Error(ctx, "failed to parse createdAt", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
					return time.Time{}, fmt.Errorf("failed to parse createdAt: %w", err)
				}
			}
		}
	}

	return created, nil
}

// GetLocalKeys func
func (s *Store) GetLocalKeys(from time.Time, to time.Time) ([]string, error) {
	var keys []string
	ctx := context.Background()
	logtrace.Info(ctx, "getting all keys for SC", logtrace.Fields{})
	if err := s.db.Select(&keys, `SELECT key FROM data WHERE createdAt > ? and createdAt < ?`, from, to); err != nil {
		return keys, fmt.Errorf("error reading all keys from database: %w", err)
	}
	logtrace.Info(ctx, "got all keys for SC", logtrace.Fields{})

	return keys, nil
}

// BatchDeleteRecords deletes a batch of records identified by their keys
func (s *Store) BatchDeleteRecords(keys []string) error {
	return batchDeleteRecords(s.db, keys)
}

func batchDeleteRecords(db *sqlx.DB, keys []string) error {
	if len(keys) == 0 {
		ctx := context.Background()
		logtrace.Info(ctx, "no keys provided for batch delete", logtrace.Fields{logtrace.FieldModule: "p2p"})
		return nil
	}

	// Create a parameter string for SQL query (?, ?, ?, ...)
	paramStr := strings.Repeat("?,", len(keys)-1) + "?"

	// Create the SQL statement
	query := fmt.Sprintf("DELETE FROM data WHERE key IN (%s)", paramStr)

	// Execute the query
	res, err := db.Exec(query, stringArgsToInterface(keys)...)
	if err != nil {
		return fmt.Errorf("cannot batch delete records: %w", err)
	}

	// Optionally check rows affected
	if rowsAffected, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("failed to get rows affected for batch delete: %w", err)
	} else if rowsAffected == 0 {
		return fmt.Errorf("no rows affected for batch delete")
	}

	return nil
}

// stringArgsToInterface converts a slice of strings to a slice of interface{}
func stringArgsToInterface(args []string) []interface{} {
	iargs := make([]interface{}, len(args))
	for i, v := range args {
		iargs[i] = v
	}
	return iargs
}

func batchSetMigratedRecords(db *sqlx.DB, keys []string) error {
	if len(keys) == 0 {
		logtrace.Info(context.Background(), "no keys provided for batch update (migrated)", logtrace.Fields{logtrace.FieldModule: "p2p"})
		return nil
	}

	// Create a parameter string for SQL query (?, ?, ?, ...)
	paramStr := strings.Repeat("?,", len(keys)-1) + "?"

	// Create the SQL statement
	query := fmt.Sprintf("UPDATE data set data = X'', is_on_cloud = true WHERE key IN (%s)", paramStr)

	// Execute the query
	res, err := db.Exec(query, stringArgsToInterface(keys)...)
	if err != nil {
		return fmt.Errorf("cannot batch update records (migrated): %w", err)
	}

	// Optionally check rows affected
	if rowsAffected, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("failed to get rows affected for batch update(migrated): %w", err)
	} else if rowsAffected == 0 {
		return fmt.Errorf("no rows affected for batch update (migrated)")
	}

	return nil
}
