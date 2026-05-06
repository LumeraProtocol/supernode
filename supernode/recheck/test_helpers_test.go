package recheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

var (
	errBoom           = errors.New("boom")
	errAlreadyOnChain = errors.New("invalid recheck evidence: recheck evidence already submitted for epoch 7 ticket \"ticket-1\" by \"self\"")
)

var callSeq int

type memoryStore struct {
	seen            map[string]bool
	failures        map[string]int
	recordCallIndex int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{seen: map[string]bool{}, failures: map[string]int{}}
}
func (m *memoryStore) HasRecheckSubmission(_ context.Context, epochID uint64, ticketID string) (bool, error) {
	return m.seen[key(epochID, ticketID)], nil
}
func (m *memoryStore) RecordPendingRecheckSubmission(_ context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error {
	callSeq++
	m.recordCallIndex = callSeq
	m.seen[key(epochID, ticketID)] = true
	return nil
}
func (m *memoryStore) MarkRecheckSubmissionSubmitted(_ context.Context, epochID uint64, ticketID string) error {
	m.seen[key(epochID, ticketID)] = true
	return nil
}
func (m *memoryStore) DeletePendingRecheckSubmission(_ context.Context, epochID uint64, ticketID string) error {
	delete(m.seen, key(epochID, ticketID))
	return nil
}
func (m *memoryStore) RecordRecheckSubmission(_ context.Context, epochID uint64, ticketID, targetAccount, challengedTranscriptHash, recheckTranscriptHash string, resultClass audittypes.StorageProofResultClass) error {
	callSeq++
	m.recordCallIndex = callSeq
	m.seen[key(epochID, ticketID)] = true
	return nil
}
func (m *memoryStore) RecordRecheckAttemptFailure(_ context.Context, epochID uint64, ticketID, targetAccount string, err error, ttl time.Duration) error {
	m.failures[key(epochID, ticketID)]++
	return nil
}
func (m *memoryStore) HasRecheckAttemptFailureBudgetExceeded(_ context.Context, epochID uint64, ticketID string, maxAttempts int) (bool, error) {
	return maxAttempts > 0 && m.failures[key(epochID, ticketID)] >= maxAttempts, nil
}
func (m *memoryStore) PurgeExpiredRecheckAttemptFailures(_ context.Context) error { return nil }
func key(epochID uint64, ticketID string) string                                  { return fmt.Sprintf("%d/%s", epochID, ticketID) }

type recordingAuditMsg struct {
	calls []submitCall
	err   error
}
type submitCall struct {
	callIndex                           int
	epochID                             uint64
	target, ticket, challenged, recheck string
	class                               audittypes.StorageProofResultClass
	details                             string
}

func (m *recordingAuditMsg) SubmitStorageRecheckEvidence(ctx context.Context, epochID uint64, challengedSupernodeAccount, ticketID, challengedResultTranscriptHash, recheckTranscriptHash string, recheckResultClass audittypes.StorageProofResultClass, details string) (*sdktx.BroadcastTxResponse, error) {
	callSeq++
	m.calls = append(m.calls, submitCall{callIndex: callSeq, epochID: epochID, target: challengedSupernodeAccount, ticket: ticketID, challenged: challengedResultTranscriptHash, recheck: recheckTranscriptHash, class: recheckResultClass, details: details})
	if m.err != nil {
		return nil, m.err
	}
	return &sdktx.BroadcastTxResponse{}, nil
}

type stubAudit struct {
	current         uint64
	reports         map[uint64]audittypes.EpochReport
	reportsBySource map[string]map[uint64]audittypes.EpochReport
	mode            audittypes.StorageTruthEnforcementMode
}

func (s *stubAudit) GetCurrentEpoch(ctx context.Context) (*audittypes.QueryCurrentEpochResponse, error) {
	return &audittypes.QueryCurrentEpochResponse{EpochId: s.current}, nil
}
func (s *stubAudit) GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*audittypes.QueryEpochReportResponse, error) {
	return &audittypes.QueryEpochReportResponse{Report: s.reports[epochID]}, nil
}
func (s *stubAudit) GetEpochReportsByReporter(ctx context.Context, reporterAccount string, epochID uint64) (*audittypes.QueryEpochReportsByReporterResponse, error) {
	if s.reportsBySource != nil {
		if byEpoch, ok := s.reportsBySource[reporterAccount]; ok {
			if report, ok := byEpoch[epochID]; ok {
				return &audittypes.QueryEpochReportsByReporterResponse{Reports: []audittypes.EpochReport{report}}, nil
			}
		}
	}
	if reporterAccount == "self" {
		return &audittypes.QueryEpochReportsByReporterResponse{Reports: []audittypes.EpochReport{s.reports[epochID]}}, nil
	}
	return &audittypes.QueryEpochReportsByReporterResponse{}, nil
}
func (s *stubAudit) GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error) {
	return &audittypes.QueryParamsResponse{Params: audittypes.Params{StorageTruthEnforcementMode: s.mode}}, nil
}

type stubRechecker struct {
	result RecheckResult
	calls  []Candidate
	err    error
}

func (s *stubRechecker) Recheck(ctx context.Context, c Candidate) (RecheckResult, error) {
	s.calls = append(s.calls, c)
	return s.result, s.err
}
