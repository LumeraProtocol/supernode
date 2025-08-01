package queries

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/pkg/configurer"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" //go-sqlite3
)

var (
	DefaulthPath = configurer.DefaultPath()
)

const minVerifications = 3
const createTaskHistory string = `
  CREATE TABLE IF NOT EXISTS task_history (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  time DATETIME NOT NULL,
  task_id TEXT NOT NULL,
  status TEXT NOT NULL
  );`

const alterTaskHistory string = `ALTER TABLE task_history ADD COLUMN details TEXT;`

const createStorageChallengeMessages string = `
  CREATE TABLE IF NOT EXISTS storage_challenge_messages (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  challenge_id TEXT NOT NULL,
  message_type INTEGER NOT NULL,
  data BLOB NOT NULL,
  sender_id TEXT NOT NULL,
  sender_signature BLOB NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);`

const createBroadcastChallengeMessages string = `
  CREATE TABLE IF NOT EXISTS broadcast_challenge_messages (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  challenge_id TEXT NOT NULL,
  challenger TEXT NOT NULL,
  recipient TEXT NOT NULL,
  observers TEXT NOT NULL,
  data BLOB NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);`

const createStorageChallengeMessagesUniqueIndex string = `
CREATE UNIQUE INDEX IF NOT EXISTS storage_challenge_messages_unique ON storage_challenge_messages(challenge_id, message_type, sender_id);
`

const createSelfHealingChallenges string = `
  CREATE TABLE IF NOT EXISTS self_healing_challenges (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  challenge_id TEXT NOT NULL,
  merkleroot TEXT NOT NULL,
  file_hash TEXT NOT NULL,
  challenging_node TEXT NOT NULL,
  responding_node TEXT NOT NULL,
  verifying_node TEXT,
  reconstructed_file_hash BLOB,
  status TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL                                                  
  );`

const createPingHistory string = `
  CREATE TABLE IF NOT EXISTS ping_history (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  supernode_id TEXT UNIQUE NOT NULL,
  ip_address TEXT UNIQUE NOT NULL,
  total_pings INTEGER NOT NULL,
  total_successful_pings INTEGER NOT NULL,
  avg_ping_response_time FLOAT NOT NULL,
  is_online BOOLEAN NOT NULL,
  is_on_watchlist BOOLEAN NOT NULL,
  is_adjusted BOOLEAN NOT NULL,
  cumulative_response_time FLOAT NOT NULL,
  last_seen DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL                                                  
  );`

const createPingHistoryUniqueIndex string = `
CREATE UNIQUE INDEX IF NOT EXISTS ping_history_unique ON ping_history(supernode_id, ip_address);
`

const createSelfHealingGenerationMetrics string = `
  CREATE TABLE IF NOT EXISTS self_healing_generation_metrics (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  trigger_id TEXT NOT NULL,
  message_type INTEGER NOT NULL,
  data BLOB NOT NULL,
  sender_id TEXT NOT NULL,
  sender_signature BLOB NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);`

const createSelfHealingGenerationMetricsUniqueIndex string = `
CREATE UNIQUE INDEX IF NOT EXISTS self_healing_generation_metrics_unique ON self_healing_generation_metrics(trigger_id);
`

const createSelfHealingExecutionMetrics string = `
  CREATE TABLE IF NOT EXISTS self_healing_execution_metrics (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  trigger_id TEXT NOT NULL,
  challenge_id TEXT NOT NULL,
  message_type INTEGER NOT NULL,
  data BLOB NOT NULL,
  sender_id TEXT NOT NULL,
  sender_signature BLOB NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);`

const createSelfHealingChallengeTickets string = `
  CREATE TABLE IF NOT EXISTS self_healing_challenge_events (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  trigger_id TEXT NOT NULL,
  ticket_id TEXT NOT NULL,
  challenge_id TEXT NOT NULL,
  data BLOB NOT NULL,
  sender_id TEXT NOT NULL,
  is_processed BOOLEAN NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
`

const createSelfHealingChallengeTicketsUniqueIndex string = `
CREATE UNIQUE INDEX IF NOT EXISTS self_healing_challenge_events_unique ON self_healing_challenge_events(trigger_id, ticket_id, challenge_id);
`

const createSelfHealingExecutionMetricsUniqueIndex string = `
CREATE UNIQUE INDEX IF NOT EXISTS self_healing_execution_metrics_unique ON self_healing_execution_metrics(trigger_id, challenge_id, message_type);
`

const alterTablePingHistory = `ALTER TABLE ping_history
ADD COLUMN metrics_last_broadcast_at DATETIME NULL;`

const alterTablePingHistoryGenerationMetrics = `ALTER TABLE ping_history
ADD COLUMN generation_metrics_last_broadcast_at DATETIME NULL;`

const alterTablePingHistoryExecutionMetrics = `ALTER TABLE ping_history
ADD COLUMN execution_metrics_last_broadcast_at DATETIME NULL;`

const createStorageChallengeMetrics string = `
  CREATE TABLE IF NOT EXISTS storage_challenge_metrics (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  challenge_id TEXT NOT NULL,
  message_type INTEGER NOT NULL,
  data BLOB NOT NULL,
  sender_id TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);`

const createStorageChallengeMetricsUniqueIndex string = `
CREATE UNIQUE INDEX IF NOT EXISTS storage_challenge_metrics_unique ON storage_challenge_metrics(challenge_id, message_type, sender_id);
`

const createHealthCheckChallengeMessages string = `
  CREATE TABLE IF NOT EXISTS healthcheck_challenge_messages (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  challenge_id TEXT NOT NULL,
  message_type INTEGER NOT NULL,
  data BLOB NOT NULL,
  sender_id TEXT NOT NULL,
  sender_signature BLOB NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);`

const createBroadcastHealthCheckChallengeMessages string = `
  CREATE TABLE IF NOT EXISTS broadcast_healthcheck_challenge_messages (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  challenge_id TEXT NOT NULL,
  challenger TEXT NOT NULL,
  recipient TEXT NOT NULL,
  observers TEXT NOT NULL,
  data BLOB NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);`

const createHealthCheckChallengeMetrics string = `
  CREATE TABLE IF NOT EXISTS healthcheck_challenge_metrics (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  challenge_id TEXT NOT NULL,
  message_type INTEGER NOT NULL,
  data BLOB NOT NULL,
  sender_id TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
`
const createHealthCheckChallengeMetricsUniqueIndex string = `
CREATE UNIQUE INDEX IF NOT EXISTS healthcheck_challenge_metrics_unique ON healthcheck_challenge_metrics(challenge_id, message_type, sender_id);
`
const alterTablePingHistoryHealthCheckColumn = `ALTER TABLE ping_history
ADD COLUMN health_check_metrics_last_broadcast_at DATETIME NULL;`

const createPingHistoryWithoutUniqueIPAddress string = `
BEGIN TRANSACTION;

CREATE TABLE IF NOT EXISTS new_ping_history (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  supernode_id TEXT UNIQUE NOT NULL,
  ip_address TEXT NOT NULL,  -- Removed UNIQUE constraint here
  total_pings INTEGER NOT NULL,
  total_successful_pings INTEGER NOT NULL,
  avg_ping_response_time FLOAT NOT NULL,
  is_online BOOLEAN NOT NULL,
  is_on_watchlist BOOLEAN NOT NULL,
  is_adjusted BOOLEAN NOT NULL,
  cumulative_response_time FLOAT NOT NULL,
  last_seen DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  metrics_last_broadcast_at DATETIME,  -- Assuming these columns already exist in the old table
  generation_metrics_last_broadcast_at DATETIME,
  execution_metrics_last_broadcast_at DATETIME,
  health_check_metrics_last_broadcast_at DATETIME
);

-- Step 2: Copy data including all columns from the old table
INSERT INTO new_ping_history (
  id,
  supernode_id,
  ip_address,
  total_pings,
  total_successful_pings,
  avg_ping_response_time,
  is_online,
  is_on_watchlist,
  is_adjusted,
  cumulative_response_time,
  last_seen,
  created_at,
  updated_at,
  metrics_last_broadcast_at,
  generation_metrics_last_broadcast_at,
  execution_metrics_last_broadcast_at,
  health_check_metrics_last_broadcast_at
)
SELECT
  id,
  supernode_id,
  ip_address,
  total_pings,
  total_successful_pings,
  avg_ping_response_time,
  is_online,
  is_on_watchlist,
  is_adjusted,
  cumulative_response_time,
  last_seen,
  created_at,
  updated_at,
  metrics_last_broadcast_at,
  generation_metrics_last_broadcast_at,
  execution_metrics_last_broadcast_at,
  health_check_metrics_last_broadcast_at
FROM ping_history;

-- Step 3: Drop the original table
DROP TABLE ping_history;

-- Step 4: Rename the new table to the original table's name
ALTER TABLE new_ping_history RENAME TO ping_history;

COMMIT;
`

const (
	historyDBName = "history.db"
	emptyString   = ""
)

// SQLiteStore handles sqlite ops
type SQLiteStore struct {
	db *sqlx.DB
}

// CloseHistoryDB closes history database
func (s *SQLiteStore) CloseHistoryDB(ctx context.Context) {
	if err := s.db.Close(); err != nil {
		logtrace.Error(ctx, "error closing history db", logtrace.Fields{
			logtrace.FieldError: err.Error()})
	}
}

// OpenHistoryDB opens history DB
func OpenHistoryDB() (LocalStoreInterface, error) {
	dbFile := filepath.Join(DefaulthPath, historyDBName)
	db, err := sqlx.Connect("sqlite3", dbFile)
	if err != nil {
		return nil, fmt.Errorf("cannot open sqlite database: %w", err)
	}

	if _, err := db.Exec(createTaskHistory); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createStorageChallengeMessages); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createStorageChallengeMessagesUniqueIndex); err != nil {
		return nil, fmt.Errorf("cannot execute migration: %w", err)
	}

	if _, err := db.Exec(createBroadcastChallengeMessages); err != nil {
		return nil, fmt.Errorf("cannot execute migration: %w", err)
	}

	if _, err := db.Exec(createSelfHealingChallenges); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createPingHistory); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createPingHistoryUniqueIndex); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createSelfHealingGenerationMetrics); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createSelfHealingGenerationMetricsUniqueIndex); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createSelfHealingExecutionMetrics); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createSelfHealingExecutionMetricsUniqueIndex); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createSelfHealingChallengeTickets); err != nil {
		return nil, fmt.Errorf("cannot create createSelfHealingChallengeTickets: %w", err)
	}

	if _, err := db.Exec(createSelfHealingChallengeTicketsUniqueIndex); err != nil {
		return nil, fmt.Errorf("cannot create createSelfHealingChallengeTicketsUniqueIndex: %w", err)
	}

	if _, err := db.Exec(createStorageChallengeMetrics); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createStorageChallengeMetricsUniqueIndex); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createHealthCheckChallengeMessages); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createHealthCheckChallengeMetrics); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createHealthCheckChallengeMetricsUniqueIndex); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	if _, err := db.Exec(createBroadcastHealthCheckChallengeMessages); err != nil {
		return nil, fmt.Errorf("cannot create table(s): %w", err)
	}

	_, _ = db.Exec(alterTaskHistory)

	_, _ = db.Exec(alterTablePingHistory)

	_, _ = db.Exec(alterTablePingHistoryGenerationMetrics)

	_, _ = db.Exec(alterTablePingHistoryExecutionMetrics)

	_, _ = db.Exec(alterTablePingHistoryHealthCheckColumn)

	_, err = db.Exec(createPingHistoryWithoutUniqueIPAddress)
	if err != nil {
		logtrace.Error(context.Background(), "error executing ping-history w/o unique ip-address constraint migration", logtrace.Fields{
			logtrace.FieldError: err.Error()})
	}

	pragmas := []string{
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA cache_size=-262144;",
		"PRAGMA busy_timeout=120000;",
		"PRAGMA journal_mode=WAL;",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("cannot set sqlite database parameter: %w", err)
		}
	}

	return &SQLiteStore{
		db: db,
	}, nil
}
