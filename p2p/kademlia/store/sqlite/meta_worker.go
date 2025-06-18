package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"os"
	"path"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/p2p/kademlia/store/cloud.go"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

var (
	commitLastAccessedInterval = 90 * time.Second
	migrationExecutionTicker   = 30 * time.Hour
	migrationMetaDB            = "data001-migration-meta.sqlite3"
	accessUpdateBufferSize     = 100000
	commitInsertsInterval      = 10 * time.Second
	metaSyncBatchSize          = 5000
	lowSpaceThresholdGB        = 50 // in GB
	minKeysToMigrate           = 100

	updateChannel chan UpdateMessage
	insertChannel chan UpdateMessage
)

func init() {
	updateChannel = make(chan UpdateMessage, accessUpdateBufferSize)
	insertChannel = make(chan UpdateMessage, accessUpdateBufferSize)
}

type UpdateMessages []UpdateMessage

// UpdateMessage holds the key and the last accessed time.
type UpdateMessage struct {
	Key            string
	LastAccessTime time.Time
	Size           int
}

// MigrationMetaStore manages database operations.
type MigrationMetaStore struct {
	db               *sqlx.DB
	p2pDataStore     *sqlx.DB
	cloud            cloud.Storage
	isSyncInProgress bool

	updateTicker             *time.Ticker
	insertTicker             *time.Ticker
	migrationExecutionTicker *time.Ticker

	updates sync.Map
	inserts sync.Map
}

// NewMigrationMetaStore initializes the MigrationMetaStore.
func NewMigrationMetaStore(ctx context.Context, dataDir string, cloud cloud.Storage) (*MigrationMetaStore, error) {
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0750); err != nil {
			return nil, fmt.Errorf("mkdir %q: %w", dataDir, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("cannot create data folder: %w", err)
	}

	dbFile := path.Join(dataDir, migrationMetaDB)
	db, err := sqlx.Connect("sqlite3", dbFile)
	if err != nil {
		return nil, fmt.Errorf("cannot open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)

	if err := setPragmas(db); err != nil {
		logtrace.Error(ctx, "error executing pragmas", logtrace.Fields{logtrace.FieldError: err})
	}

	p2pDataStore, err := connectP2PDataStore(dataDir)
	if err != nil {
		logtrace.Error(ctx, "error connecting p2p store from meta-migration store", logtrace.Fields{logtrace.FieldError: err})
	}

	handler := &MigrationMetaStore{
		db:                       db,
		p2pDataStore:             p2pDataStore,
		updateTicker:             time.NewTicker(commitLastAccessedInterval),
		insertTicker:             time.NewTicker(commitInsertsInterval),
		migrationExecutionTicker: time.NewTicker(migrationExecutionTicker),
		cloud:                    cloud,
	}

	if err := handler.migrateMeta(); err != nil {
		logtrace.Error(ctx, "cannot create meta table in sqlite database", logtrace.Fields{logtrace.FieldError: err, logtrace.FieldModule: "p2p"})
	}

	if err := handler.migrateMetaMigration(); err != nil {
		logtrace.Error(ctx, "cannot create meta-migration table in sqlite database", logtrace.Fields{logtrace.FieldError: err, logtrace.FieldModule: "p2p"})
	}

	if err := handler.migrateMigration(); err != nil {
		logtrace.Error(ctx, "cannot create migration table in sqlite database", logtrace.Fields{logtrace.FieldError: err, logtrace.FieldModule: "p2p"})
	}

	go func() {
		if handler.isMetaSyncRequired() {
			handler.isSyncInProgress = true
			err := handler.syncMetaWithData(ctx)
			if err != nil {
				logtrace.Error(ctx, "error syncing meta with p2p data", logtrace.Fields{logtrace.FieldError: err})
			}

			handler.isSyncInProgress = false
		}
	}()

	go handler.startLastAccessedUpdateWorker(ctx)
	go handler.startInsertWorker(ctx)
	go handler.startMigrationExecutionWorker(ctx)
	logtrace.Info(ctx, "MigrationMetaStore workers started", logtrace.Fields{})

	return handler, nil
}

func setPragmas(db *sqlx.DB) error {
	// Set journal mode to WAL
	_, err := db.Exec("PRAGMA journal_mode=WAL;")
	if err != nil {
		return err
	}
	// Set synchronous to NORMAL
	_, err = db.Exec("PRAGMA synchronous=NORMAL;")
	if err != nil {
		return err
	}

	// Set cache size
	_, err = db.Exec("PRAGMA cache_size=-262144;")
	if err != nil {
		return err
	}

	// Set busy timeout
	_, err = db.Exec("PRAGMA busy_timeout=5000;")
	if err != nil {
		return err
	}

	return nil
}

func (d *MigrationMetaStore) migrateMeta() error {
	query := `
	CREATE TABLE IF NOT EXISTS meta (
		key TEXT PRIMARY KEY,
		last_accessed TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		access_count INTEGER DEFAULT 0,
		data_size INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	); `
	if _, err := d.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create table 'del_keys': %w", err)
	}

	return nil
}

func (d *MigrationMetaStore) migrateMetaMigration() error {
	query := `
	CREATE TABLE IF NOT EXISTS meta_migration (
		key TEXT,
		migration_id INTEGER,
		score INTEGER,
		is_migrated BOOLEAN,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (key, migration_id)
	);`
	if _, err := d.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create table 'del_keys': %w", err)
	}

	return nil
}

func (d *MigrationMetaStore) migrateMigration() error {
	query := `
	CREATE TABLE IF NOT EXISTS migration (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		total_data_size INTEGER,
		migration_started_at TIMESTAMP,
		migration_finished_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := d.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create table 'del_keys': %w", err)
	}

	return nil
}

func (d *MigrationMetaStore) isMetaSyncRequired() bool {
	var exists int
	query := `SELECT EXISTS(SELECT 1 FROM meta LIMIT 1);`

	err := d.db.QueryRow(query).Scan(&exists)
	if err != nil {
		return false
	}

	return exists == 0
}

func (d *MigrationMetaStore) syncMetaWithData(ctx context.Context) error {
	var offset int

	query := `SELECT key, data, updatedAt FROM data LIMIT ? OFFSET ?`
	insertQuery := `
	INSERT INTO meta (key, last_accessed, access_count, data_size)
	VALUES (?, ?, 1, ?)
	ON CONFLICT(key) DO
	UPDATE SET
	last_accessed = EXCLUDED.last_accessed,
	    data_size = EXCLUDED.data_size,
		access_count = access_count + 1`

	continueProcessing := true
	for continueProcessing {
		rows, err := d.p2pDataStore.Queryx(query, metaSyncBatchSize, offset)
		if err != nil {
			logtrace.Error(ctx, "error querying p2p data store", logtrace.Fields{logtrace.FieldError: err})
			break
		}

		tx, err := d.db.Beginx()
		if err != nil {
			rows.Close()
			logtrace.Error(ctx, "failed to start transaction", logtrace.Fields{logtrace.FieldError: err})
			continue
		}

		stmt, err := tx.Prepare(insertQuery)
		if err != nil {
			tx.Rollback()
			rows.Close()
			logtrace.Error(ctx, "failed to prepare statement", logtrace.Fields{logtrace.FieldError: err})
			continue
		}

		var recordsProcessed int
		for rows.Next() {
			var r Record
			var t *time.Time

			if err := rows.Scan(&r.Key, &r.Data, &t); err != nil {
				logtrace.Error(ctx, "error scanning row from p2p data store", logtrace.Fields{logtrace.FieldError: err})
				continue
			}
			if t != nil {
				r.UpdatedAt = *t
			}

			if _, err := stmt.Exec(r.Key, r.UpdatedAt, len(r.Data)); err != nil {
				logtrace.Error(ctx, "error inserting key to meta", logtrace.Fields{"key": r.Key, logtrace.FieldError: err})
				continue
			}

			recordsProcessed++
		}

		if err := rows.Err(); err != nil {
			logtrace.Error(ctx, "error iterating rows", logtrace.Fields{logtrace.FieldError: err})
		}

		if recordsProcessed > 0 {
			if err := tx.Commit(); err != nil {
				logtrace.Error(ctx, "Failed to commit transaction", logtrace.Fields{logtrace.FieldError: err})
			}
		} else {
			tx.Rollback()
		}

		rows.Close()
		stmt.Close()

		if recordsProcessed == 0 {
			continueProcessing = false // No more records to process
		} else {
			offset += metaSyncBatchSize
		}
	}

	return nil
}

func connectP2PDataStore(dataDir string) (*sqlx.DB, error) {
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0750); err != nil {
			return nil, fmt.Errorf("mkdir %q: %w", dataDir, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("cannot create data folder: %w", err)
	}

	dbFile := path.Join(dataDir, dbName)
	dataDb, err := sqlx.Connect("sqlite3", dbFile)
	if err != nil {
		return nil, fmt.Errorf("cannot open sqlite database: %w", err)
	}
	dataDb.SetMaxOpenConns(200)
	dataDb.SetMaxIdleConns(10)

	return dataDb, nil
}

// PostAccessUpdate sends access updates to be handled by the worker.
func PostAccessUpdate(updates []string) {
	for _, update := range updates {
		select {
		case updateChannel <- UpdateMessage{
			Key:            update,
			LastAccessTime: time.Now().UTC(),
		}:
			// Inserted
		default:
			logtrace.Error(context.Background(), "updateChannel is full, dropping update", logtrace.Fields{})
		}

	}
}

// startWorker listens for updates and commits them periodically.
func (d *MigrationMetaStore) startLastAccessedUpdateWorker(ctx context.Context) {
	for {
		select {
		case update := <-updateChannel:
			d.updates.Store(update.Key, update.LastAccessTime)

		case <-d.updateTicker.C:
			d.commitLastAccessedUpdates(ctx)
		case <-ctx.Done():
			logtrace.Info(ctx, "Shutting down last accessed update worker", logtrace.Fields{})
			return
		}
	}
}

func (d *MigrationMetaStore) commitLastAccessedUpdates(ctx context.Context) {
	keysToUpdate := make(map[string]time.Time)
	d.updates.Range(func(key, value interface{}) bool {
		k, ok := key.(string)
		if !ok {
			logtrace.Error(ctx, "Error converting key to string (commitLastAccessedUpdates)", logtrace.Fields{})
			return false
		}
		v, ok := value.(time.Time)
		if !ok {
			logtrace.Error(ctx, "Error converting value to time.Time (commitLastAccessedUpdates)", logtrace.Fields{})
			return false
		}
		keysToUpdate[k] = v

		return true // continue
	})

	if len(keysToUpdate) == 0 {
		return
	}

	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		logtrace.Error(ctx, "Error starting transaction (commitLastAccessedUpdates)", logtrace.Fields{logtrace.FieldError: err})
		return
	}

	stmt, err := tx.Prepare(`
	INSERT INTO meta (key, last_accessed, access_count) 
	VALUES (?, ?, 1) 
	ON CONFLICT(key) DO 
	UPDATE SET 
		last_accessed = EXCLUDED.last_accessed,
		access_count = access_count + 1`)
	if err != nil {
		tx.Rollback() // Roll back the transaction on error
		logtrace.Error(ctx, "Error preparing statement (commitLastAccessedUpdates)", logtrace.Fields{logtrace.FieldError: err})
		return
	}
	defer stmt.Close()

	for k, v := range keysToUpdate {
		_, err := stmt.Exec(k, v)
		if err != nil {
			logtrace.Error(ctx, "Error executing statement (commitLastAccessedUpdates)", logtrace.Fields{logtrace.FieldError: err, "key": k})
		}
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		logtrace.Error(ctx, "Error committing transaction (commitLastAccessedUpdates)", logtrace.Fields{logtrace.FieldError: err})
		return
	}

	// Clear updated keys from map after successful commit
	for k := range keysToUpdate {
		d.updates.Delete(k)
	}

	logtrace.Info(ctx, "Committed last accessed updates", logtrace.Fields{"count": len(keysToUpdate)})
}

func PostKeysInsert(updates []UpdateMessage) {
	for _, update := range updates {
		select {
		case insertChannel <- update:
			// Inserted
		default:
			logtrace.Error(context.Background(), "insertChannel is full, dropping update", logtrace.Fields{})
		}
	}
}

// startInsertWorker listens for updates and commits them periodically.
func (d *MigrationMetaStore) startInsertWorker(ctx context.Context) {
	for {
		select {
		case update := <-insertChannel:
			d.inserts.Store(update.Key, update)
		case <-d.insertTicker.C:
			d.commitInserts(ctx)
		case <-ctx.Done():
			logtrace.Info(ctx, "Shutting down insert meta keys worker", logtrace.Fields{})
			d.commitInserts(ctx)
			return
		}
	}
}

func (d *MigrationMetaStore) commitInserts(ctx context.Context) {
	keysToUpdate := make(map[string]UpdateMessage)
	d.inserts.Range(func(key, value interface{}) bool {
		k, ok := key.(string)
		if !ok {
			logtrace.Error(ctx, "Error converting key to string (commitInserts)", logtrace.Fields{})
			return false
		}
		v, ok := value.(UpdateMessage)
		if !ok {
			logtrace.Error(ctx, "Error converting value to UpdateMessage (commitInserts)", logtrace.Fields{})
			return false
		}

		keysToUpdate[k] = v

		return true // continue
	})

	if len(keysToUpdate) == 0 {
		return
	}

	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		logtrace.Error(ctx, "Error starting transaction (commitInserts)", logtrace.Fields{logtrace.FieldError: err})
		return
	}

	// Prepare an INSERT OR REPLACE statement that handles new insertions or updates existing entries
	stmt, err := tx.Preparex("INSERT OR REPLACE INTO meta (key, last_accessed, access_count, data_size) VALUES (?, ?, ?, ?)")
	if err != nil {
		tx.Rollback() // Ensure to rollback in case of an error
		logtrace.Error(ctx, "Error preparing statement (commitInserts)", logtrace.Fields{logtrace.FieldError: err})
		return
	}
	defer stmt.Close()

	for k, v := range keysToUpdate {
		accessCount := 1
		_, err := stmt.Exec(k, v.LastAccessTime, accessCount, v.Size)
		if err != nil {
			logtrace.Error(ctx, "Error executing statement (commitInserts)", logtrace.Fields{logtrace.FieldError: err, "key": k})
		}
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback() // Rollback transaction if commit fails
		logtrace.Error(ctx, "Error committing transaction (commitInserts)", logtrace.Fields{logtrace.FieldError: err})
		return
	}

	// Clear updated keys from map after successful commit
	for k := range keysToUpdate {
		d.inserts.Delete(k)
	}

	logtrace.Info(ctx, "Committed inserts", logtrace.Fields{"count": len(keysToUpdate)})
}

// startMigrationExecutionWorker starts the worker that executes a migration
func (d *MigrationMetaStore) startMigrationExecutionWorker(ctx context.Context) {
	for {
		select {
		case <-d.migrationExecutionTicker.C:
			d.checkAndExecuteMigration(ctx)
		case <-ctx.Done():
			logtrace.Info(ctx, "Shutting down data migration worker", logtrace.Fields{})
			return
		}
	}
}

type Migration struct {
	ID                  int          `db:"id"`
	MigrationStartedAt  sql.NullTime `db:"migration_started_at"`
	MigrationFinishedAt sql.NullTime `db:"migration_finished_at"`
}
type Migrations []Migration

type MigrationKey struct {
	Key         string `db:"key"`
	MigrationID int    `db:"migration_id"`
	IsMigrated  bool   `db:"is_migrated"`
}

type MigrationKeys []MigrationKey

func (d *MigrationMetaStore) checkAndExecuteMigration(ctx context.Context) {
	// Check the available disk space
	isLow, err := utils.CheckDiskSpace(lowSpaceThresholdGB)
	if err != nil {
		logtrace.Error(ctx, "migration worker: check disk space failed", logtrace.Fields{logtrace.FieldError: err})
	}

	//if !isLow {
	// Disk space is sufficient, stop migration
	//return
	//}

	logtrace.Info(ctx, "Starting data migration", logtrace.Fields{"islow": isLow})
	// Step 1: Fetch pending migrations
	var migrations Migrations

	err = d.db.Select(&migrations, `SELECT id FROM migration WHERE migration_started_at IS NULL or migration_finished_at IS NULL`)
	if err != nil {
		logtrace.Error(ctx, "Failed to fetch pending migrations", logtrace.Fields{logtrace.FieldError: err})
		return
	}
	logtrace.Info(ctx, "Fetched pending migrations", logtrace.Fields{"count": len(migrations)})

	// Iterate over each migration
	for _, migration := range migrations {
		logtrace.Info(ctx, "Processing migration", logtrace.Fields{"migration_id": migration.ID})

		if err := d.ProcessMigrationInBatches(ctx, migration); err != nil {
			logtrace.Error(ctx, "Failed to process migration", logtrace.Fields{logtrace.FieldError: err, "migration_id": migration.ID})
			continue
		}
	}
}

func (d *MigrationMetaStore) ProcessMigrationInBatches(ctx context.Context, migration Migration) error {
	var migKeys MigrationKeys
	err := d.db.Select(&migKeys, `SELECT key FROM meta_migration WHERE migration_id = ?`, migration.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch keys for migration %d: %w", migration.ID, err)
	}

	totalKeys := len(migKeys)
	if totalKeys == 0 {
		return nil
	}

	if totalKeys < minKeysToMigrate {
		logtrace.Info(ctx, "Skipping migration due to insufficient keys", logtrace.Fields{"migration_id": migration.ID, "keys-count": totalKeys})
		return nil
	}

	migratedKeys := 0
	var keys []string
	for _, key := range migKeys {
		if key.IsMigrated {
			migratedKeys++
		} else {
			keys = append(keys, key.Key)
		}
	}

	nonMigratedKeys := len(keys)
	maxBatchSize := 5000
	for start := 0; start < nonMigratedKeys; start += maxBatchSize {
		end := start + maxBatchSize
		if end > nonMigratedKeys {
			end = nonMigratedKeys
		}

		batchKeys := keys[start:end]

		// Mark migration as started
		if start == 0 && !migration.MigrationStartedAt.Valid {
			if _, err := d.db.Exec(`UPDATE migration SET migration_started_at = ? WHERE id = ?`, time.Now(), migration.ID); err != nil {
				return err
			}
		}

		// Retrieve and upload data for current batch
		if err := d.processSingleBatch(ctx, batchKeys); err != nil {
			logtrace.Error(ctx, "Failed to process batch", logtrace.Fields{logtrace.FieldError: err, "migration_id": migration.ID, "batch-keys-count": len(batchKeys)})
			return fmt.Errorf("failed to process batch: %w - exiting now", err)
		}
	}

	// Mark migration as finished if all keys are migrated
	var leftMigKeys MigrationKeys
	err = d.db.Select(&leftMigKeys, `SELECT key FROM meta_migration WHERE migration_id = ? and is_migrated = ?`, migration.ID, false)
	if err != nil {
		return fmt.Errorf("failed to fetch keys for migration %d: %w", migration.ID, err)
	}

	if len(leftMigKeys) == 0 {
		if _, err := d.db.Exec(`UPDATE migration SET migration_finished_at = ? WHERE id = ?`, time.Now(), migration.ID); err != nil {
			return fmt.Errorf("failed to mark migration %d as finished: %w", migration.ID, err)
		}
	}

	logtrace.Info(ctx, "Migration processed successfully", logtrace.Fields{"migration_id": migration.ID, "tota-keys-count": totalKeys, "migrated_in_current_iteration": nonMigratedKeys})

	return nil
}

func (d *MigrationMetaStore) processSingleBatch(ctx context.Context, keys []string) error {
	values, nkeys, err := d.retrieveBatchValuesToMigrate(ctx, keys)
	if err != nil {
		return fmt.Errorf("failed to retrieve batch values: %w", err)
	}

	if err := d.uploadInBatches(ctx, nkeys, values); err != nil {
		return fmt.Errorf("failed to upload batch values: %w", err)
	}

	return nil
}

func (d *MigrationMetaStore) uploadInBatches(ctx context.Context, keys []string, values [][]byte) error {
	const batchSize = 1000
	totalItems := len(keys)
	batches := (totalItems + batchSize - 1) / batchSize // Calculate number of batches needed
	var lastError error

	for i := 0; i < batches; i++ {
		start := i * batchSize
		end := start + batchSize
		if end > totalItems {
			end = totalItems
		}

		batchKeys := keys[start:end]
		batchData := values[start:end]

		uploadedKeys, err := d.cloud.UploadBatch(batchKeys, batchData)
		if err != nil {
			logtrace.Error(ctx, "Failed to upload batch", logtrace.Fields{logtrace.FieldError: err, "batch": i + 1})
			lastError = err
			continue
		}

		if err := batchSetMigratedRecords(d.p2pDataStore, uploadedKeys); err != nil {
			logtrace.Error(ctx, "Failed to delete batch", logtrace.Fields{logtrace.FieldError: err, "batch": i + 1})
			lastError = err
			continue
		}

		if err := d.batchSetMigrated(uploadedKeys); err != nil {
			logtrace.Error(ctx, "Failed to batch is_migrated", logtrace.Fields{logtrace.FieldError: err, "batch": i + 1})
			lastError = err
			continue
		}

		logtrace.Info(ctx, "Successfully uploaded and deleted records for batch", logtrace.Fields{"batch": i + 1, "total_batches": batches})
	}

	return lastError
}

type MetaStoreInterface interface {
	GetCountOfStaleData(ctx context.Context, staleTime time.Time) (int, error)
	GetStaleDataInBatches(ctx context.Context, batchSize, batchNumber int, duration time.Time) ([]string, error)
	GetPendingMigrationID(ctx context.Context) (int, error)
	CreateNewMigration(ctx context.Context) (int, error)
	InsertMetaMigrationData(ctx context.Context, migrationID int, keys []string) error
}

// GetCountOfStaleData returns the count of stale data where last_accessed is 3 months before.
func (d *MigrationMetaStore) GetCountOfStaleData(ctx context.Context, staleTime time.Time) (int, error) {
	var count int
	query := `SELECT COUNT(*)
FROM meta m
LEFT JOIN (
    SELECT DISTINCT mm.key
    FROM meta_migration mm
    JOIN migration mg ON mm.migration_id = mg.id
    WHERE mg.migration_started_at > $1
    AND mm.is_migrated = true   
       OR mm.created_at > $1
) AS recent_migrations ON m.key = recent_migrations.key
WHERE m.last_accessed < $1
AND recent_migrations.key IS NULL;`

	err := d.db.GetContext(ctx, &count, query, staleTime)
	if err != nil {
		return 0, fmt.Errorf("failed to get count of stale data: %w", err)
	}
	return count, nil
}

// GetStaleDataInBatches retrieves stale data entries in batches from the meta table.
func (d *MigrationMetaStore) GetStaleDataInBatches(ctx context.Context, batchSize, batchNumber int, duration time.Time) ([]string, error) {
	offset := batchNumber * batchSize

	query := `
SELECT m.key
FROM meta m
LEFT JOIN (
    SELECT DISTINCT mm.key
    FROM meta_migration mm
    JOIN migration mg ON mm.migration_id = mg.id
    WHERE mg.migration_started_at > $1 
    AND mm.is_migrated = true   
       OR mm.created_at > $1
) AS recent_migrations ON m.key = recent_migrations.key
WHERE m.last_accessed < $1
AND recent_migrations.key IS NULL
LIMIT $2 OFFSET $3;`

	rows, err := d.db.QueryxContext(ctx, query, duration, batchSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get stale data in batches: %w", err)
	}
	defer rows.Close()

	var staleData []string
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		staleData = append(staleData, data)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate over rows: %w", err)
	}
	return staleData, nil
}

func (d *MigrationMetaStore) GetPendingMigrationID(ctx context.Context) (int, error) {
	var migrationID int
	query := `SELECT id FROM migration WHERE migration_started_at IS NULL LIMIT 1`

	err := d.db.GetContext(ctx, &migrationID, query)
	if err == sql.ErrNoRows {
		return 0, nil // No pending migrations
	} else if err != nil {
		return 0, fmt.Errorf("failed to get pending migration ID: %w", err)
	}

	return migrationID, nil
}

func (d *MigrationMetaStore) CreateNewMigration(ctx context.Context) (int, error) {
	query := `INSERT INTO migration (created_at, updated_at) VALUES (?, ?)`
	now := time.Now()

	result, err := d.db.ExecContext(ctx, query, now, now)
	if err != nil {
		return 0, fmt.Errorf("failed to create new migration: %w", err)
	}

	migrationID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}

	return int(migrationID), nil
}

func (d *MigrationMetaStore) InsertMetaMigrationData(ctx context.Context, migrationID int, keys []string) error {
	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	stmt, err := tx.Preparex(`INSERT INTO meta_migration (key, migration_id, created_at, updated_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	now := time.Now()
	for _, key := range keys {
		if _, err := stmt.Exec(key, migrationID, now, now); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert meta migration data: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (d *MigrationMetaStore) batchSetMigrated(keys []string) error {
	if len(keys) == 0 {
		// log.P2P().Info("no keys provided for batch update (is_migrated)")
		logtrace.Info(context.Background(), "No keys provided for batch update (is_migrated)", logtrace.Fields{})
		return nil
	}

	// Create a parameter string for SQL query (?, ?, ?, ...)
	paramStr := strings.Repeat("?,", len(keys)-1) + "?"

	// Create the SQL statement
	query := fmt.Sprintf("UPDATE meta_migration set is_migrated = true WHERE key IN (%s)", paramStr)

	// Execute the query
	res, err := d.db.Exec(query, stringArgsToInterface(keys)...)
	if err != nil {
		return fmt.Errorf("cannot batch update records (is_migrated): %w", err)
	}

	// Optionally check rows affected
	if rowsAffected, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("failed to get rows affected for batch update(is_migrated): %w", err)
	} else if rowsAffected == 0 {
		return fmt.Errorf("no rows affected for batch update (is_migrated)")
	}

	return nil
}

func (d *MigrationMetaStore) retrieveBatchValuesToMigrate(ctx context.Context, keys []string) ([][]byte, []string, error) {
	if len(keys) == 0 {
		return [][]byte{}, []string{}, nil // Return empty if no keys provided
	}

	placeholders := make([]string, len(keys))
	args := make([]interface{}, len(keys))
	for i, key := range keys {
		placeholders[i] = "?"
		args[i] = key
	}

	query := fmt.Sprintf(`SELECT key, data, is_on_cloud FROM data WHERE key IN (%s) AND is_on_cloud = false`, strings.Join(placeholders, ","))
	rows, err := d.p2pDataStore.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to retrieve records: %w", err)
	}
	defer rows.Close()

	// Assume pre-allocation for efficiency
	values := make([][]byte, 0, len(keys))
	foundKeys := make([]string, 0, len(keys))

	for rows.Next() {
		var key string
		var value []byte
		var isOnCloud bool
		if err := rows.Scan(&key, &value, &isOnCloud); err != nil {
			return nil, nil, fmt.Errorf("failed to scan key and value: %w", err)
		}
		if len(value) > 1 && !isOnCloud {
			values = append(values, value)
			foundKeys = append(foundKeys, key)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, foundKeys, fmt.Errorf("rows processing error: %w", err)
	}

	return values, foundKeys, nil
}
