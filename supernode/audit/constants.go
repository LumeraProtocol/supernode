package audit

import "time"

const (
	EnabledByDefault = true

	SQLiteFilename = "audit.db"
)

const (
	// Polling too frequently can overload chain gRPC; the audit window cadence is much slower.
	CurrentWindowPollInterval = 5 * time.Minute

	ProbeTimeout          = 5 * time.Second
	MaxConcurrentProbes   = 10
	SubmitRetryBackoff    = 15 * time.Second
	InitialStartupDelay   = 2 * time.Second
	DBBusyTimeout         = 120 * time.Second
	DBCacheSizeKiB        = 256 * 1024
)

const (
	APIPort    uint32 = 4444
	P2PPort    uint32 = 4445
	StatusPort uint32 = 8002
)
