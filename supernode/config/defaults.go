package config

import "time"

// Centralized default values for configuration

const (
	DefaultKeyringBackend                 = "test"
	DefaultKeyringDir                     = "keys"
	DefaultKeyName                        = "test-key"
	DefaultSupernodeHost                  = "0.0.0.0"
	DefaultSupernodePort                  = 4444
	DefaultP2PPort                        = 4445
	DefaultLumeraGRPC                     = "localhost:9090"
	DefaultChainID                        = "testing"
	DefaultRaptorQFilesDir                = "raptorq_files"
	DefaultStorageChallengePollIntervalMs = 5000

	DefaultLEP6MaxConcurrentTargets = 4
	DefaultLEP6RecipientReadTimeout = 30 * time.Second

	DefaultLEP6RecheckLookbackEpochs              = uint64(7)
	DefaultLEP6RecheckMaxPerTick                  = 5
	DefaultLEP6RecheckTickInterval                = time.Minute
	DefaultLEP6RecheckMaxFailureAttemptsPerTicket = 3
	DefaultLEP6RecheckFailureBackoffTTL           = 15 * time.Minute

	DefaultSelfHealingPollInterval               = 30 * time.Second
	DefaultSelfHealingMaxConcurrentReconstructs  = 2
	DefaultSelfHealingMaxConcurrentVerifications = 4
	DefaultSelfHealingMaxConcurrentPublishes     = 2
	DefaultSelfHealingStagingDir                 = "heal-staging"
	DefaultSelfHealingVerifierFetchTimeout       = 60 * time.Second
	DefaultSelfHealingVerifierFetchAttempts      = 3
	DefaultSelfHealingVerifierBackoffBase        = 2 * time.Second
	DefaultSelfHealingAuditQueryTimeout          = 10 * time.Second
)
