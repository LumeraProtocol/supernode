package recheck

import (
	"context"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

const (
	DefaultLookbackEpochs              = uint64(7)
	DefaultMaxPerTick                  = 5
	DefaultTickInterval                = time.Minute
	DefaultMaxFailureAttemptsPerTicket = 3
	DefaultFailureBackoffTTL           = 15 * time.Minute
)

type Outcome int

const (
	OutcomePass Outcome = iota
	OutcomeConfirmedHashMismatch
	OutcomeTimeout
	OutcomeObserverQuorumFail
	OutcomeInvalidTranscript
)

type Candidate struct {
	EpochID                  uint64
	TargetAccount            string
	TicketID                 string
	ChallengedTranscriptHash string
	OriginalReporter         string
	OriginalResultClass      audittypes.StorageProofResultClass
}

type RecheckResult struct {
	TranscriptHash string
	ResultClass    audittypes.StorageProofResultClass
	Details        string
}

type Store interface {
	HasRecheckSubmission(ctx context.Context, epochID uint64, ticketID string) (bool, error)
	RecordPendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error
	MarkRecheckSubmissionSubmitted(ctx context.Context, epochID uint64, ticketID string) error
	DeletePendingRecheckSubmission(ctx context.Context, epochID uint64, ticketID string) error
	RecordRecheckSubmission(ctx context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error
	RecordRecheckAttemptFailure(ctx context.Context, epochID uint64, ticketID, targetAccount string, err error, ttl time.Duration) error
	HasRecheckAttemptFailureBudgetExceeded(ctx context.Context, epochID uint64, ticketID string, maxAttempts int) (bool, error)
	PurgeExpiredRecheckAttemptFailures(ctx context.Context) error
}

type AuditReader interface {
	GetCurrentEpoch(ctx context.Context) (*audittypes.QueryCurrentEpochResponse, error)
	GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*audittypes.QueryEpochReportResponse, error)
	GetEpochReportsByReporter(ctx context.Context, reporterAccount string, epochID uint64) (*audittypes.QueryEpochReportsByReporterResponse, error)
	GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error)
}

type ReporterSource interface {
	ReporterAccounts(ctx context.Context) ([]string, error)
}

type Rechecker interface {
	Recheck(ctx context.Context, candidate Candidate) (RecheckResult, error)
}

func IsRecheckEligibleResultClass(cls audittypes.StorageProofResultClass) bool {
	switch cls {
	case audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_OBSERVER_QUORUM_FAIL,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT:
		return true
	default:
		return false
	}
}

func MapRecheckOutcome(outcome Outcome) audittypes.StorageProofResultClass {
	switch outcome {
	case OutcomePass:
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS
	case OutcomeConfirmedHashMismatch:
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL
	case OutcomeTimeout:
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE
	case OutcomeObserverQuorumFail:
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_OBSERVER_QUORUM_FAIL
	case OutcomeInvalidTranscript:
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT
	default:
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT
	}
}

func (c Candidate) Valid() bool {
	return c.EpochID > 0 && strings.TrimSpace(c.TargetAccount) != "" && strings.TrimSpace(c.TicketID) != "" && strings.TrimSpace(c.ChallengedTranscriptHash) != "" && strings.TrimSpace(c.OriginalReporter) != "" && IsRecheckEligibleResultClass(c.OriginalResultClass)
}
