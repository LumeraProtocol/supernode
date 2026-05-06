package recheck

import (
	"context"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

func TestFinder_LookbackLimitDedupAndOrder(t *testing.T) {
	store := newMemoryStore()
	require.NoError(t, store.RecordRecheckSubmission(context.Background(), 9, "done", "target", "h", "rh", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS))
	a := &stubAudit{current: 10, reports: map[uint64]audittypes.EpochReport{
		10: {StorageProofResults: []*audittypes.StorageProofResult{
			resFrom("peer", "z", "target-z", "hz", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
			resFrom("peer", "pass", "target-pass", "hp", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS),
		}},
		9: {StorageProofResults: []*audittypes.StorageProofResult{
			resFrom("peer", "done", "target-done", "hd", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
			resFrom("peer", "a", "target-a", "ha", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE),
		}},
		2: {StorageProofResults: []*audittypes.StorageProofResult{
			resFrom("peer", "too-old", "target-old", "ho", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
		}},
	}}
	f := NewFinder(a, store, "self", FinderConfig{LookbackEpochs: 2, MaxPerTick: 2})
	got, err := f.Find(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, uint64(10), got[0].EpochID)
	require.Equal(t, "z", got[0].TicketID)
	require.Equal(t, uint64(9), got[1].EpochID)
	require.Equal(t, "a", got[1].TicketID)
}

func TestFinder_ScansNetworkReporterSetNotSelfOnly(t *testing.T) {
	store := newMemoryStore()
	a := &stubAudit{current: 5, reportsBySource: map[string]map[uint64]audittypes.EpochReport{
		"self": {
			5: {StorageProofResults: []*audittypes.StorageProofResult{
				resFrom("other", "self-ticket", "target-self", "h-self", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
			}},
		},
		"peer-reporter": {
			5: {StorageProofResults: []*audittypes.StorageProofResult{
				{TargetSupernodeAccount: "target-peer", ChallengerSupernodeAccount: "peer-reporter", TicketId: "peer-ticket", TranscriptHash: "h-peer", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE},
			}},
		},
	}}
	f := NewFinderWithReporters(a, store, "self", FinderConfig{LookbackEpochs: 1, MaxPerTick: 10}, NewStaticReporterSource("self", "peer-reporter"))
	got, err := f.Find(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "peer-ticket", got[0].TicketID)
	require.Equal(t, "peer-reporter", got[0].OriginalReporter)
	require.Equal(t, "self-ticket", got[1].TicketID)
}

func TestFinder_SkipsSelfTargetCandidate(t *testing.T) {
	store := newMemoryStore()
	a := &stubAudit{current: 5, reportsBySource: map[string]map[uint64]audittypes.EpochReport{
		"peer-reporter": {
			5: {StorageProofResults: []*audittypes.StorageProofResult{
				resFrom("peer-reporter", "against-self", "self", "h-self", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
				resFrom("peer-reporter", "against-peer", "target-peer", "h-peer", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
			}},
		},
	}}
	f := NewFinderWithReporters(a, store, "self", FinderConfig{LookbackEpochs: 1, MaxPerTick: 10}, NewStaticReporterSource("peer-reporter"))
	got, err := f.Find(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "against-peer", got[0].TicketID)
}

func TestFinder_SkipsSelfReportedCandidate(t *testing.T) {
	store := newMemoryStore()
	a := &stubAudit{current: 5, reportsBySource: map[string]map[uint64]audittypes.EpochReport{
		"self": {
			5: {StorageProofResults: []*audittypes.StorageProofResult{
				resFrom("self", "own-report", "target-peer", "h-own", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
				resFrom("peer-reporter", "peer-report", "target-peer", "h-peer", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH),
			}},
		},
	}}
	f := NewFinderWithReporters(a, store, "self", FinderConfig{LookbackEpochs: 1, MaxPerTick: 10}, NewStaticReporterSource("self"))
	got, err := f.Find(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "peer-report", got[0].TicketID)
}

func TestService_TickModeGateAndSubmit(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{}
	a := &stubAudit{current: 10, mode: audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED, reports: map[uint64]audittypes.EpochReport{10: {StorageProofResults: []*audittypes.StorageProofResult{resFrom("peer", "t", "target", "h", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH)}}}}
	r := &stubRechecker{result: RecheckResult{TranscriptHash: "rh", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS}}
	svc, err := NewService(Config{Enabled: true, TickInterval: time.Millisecond}, a, store, r, NewAttestor("self", msg, store), "self")
	require.NoError(t, err)
	require.NoError(t, svc.Tick(ctx))
	require.Empty(t, msg.calls)

	a.mode = audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL
	require.NoError(t, svc.Tick(ctx))
	require.Len(t, msg.calls, 1)
	require.Equal(t, "target", msg.calls[0].target)
}

func TestService_TickSkipsRecheckWhenFailureBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	store.failures[key(10, "t")] = 2
	msg := &recordingAuditMsg{}
	a := &stubAudit{current: 10, mode: audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL, reports: map[uint64]audittypes.EpochReport{10: {StorageProofResults: []*audittypes.StorageProofResult{resFrom("peer", "t", "target", "h", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH)}}}}
	r := &stubRechecker{result: RecheckResult{TranscriptHash: "rh", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS}}
	svc, err := NewService(Config{Enabled: true, TickInterval: time.Millisecond, MaxFailureAttemptsPerTicket: 2}, a, store, r, NewAttestor("self", msg, store), "self")
	require.NoError(t, err)

	require.NoError(t, svc.Tick(ctx))
	require.Empty(t, r.calls, "recheck execution should be skipped after the per-ticket failure budget is exhausted")
	require.Empty(t, msg.calls, "no chain submission should be attempted for a budget-blocked candidate")
}

func TestConfigDefaults(t *testing.T) {
	got := (Config{}).WithDefaults()
	require.Equal(t, DefaultLookbackEpochs, got.LookbackEpochs)
	require.Equal(t, DefaultMaxPerTick, got.MaxPerTick)
	require.Equal(t, DefaultTickInterval, got.TickInterval)
}

func res(ticket, target, transcript string, class audittypes.StorageProofResultClass) *audittypes.StorageProofResult {
	return resFrom("self", ticket, target, transcript, class)
}

func resFrom(reporter, ticket, target, transcript string, class audittypes.StorageProofResultClass) *audittypes.StorageProofResult {
	return &audittypes.StorageProofResult{TargetSupernodeAccount: target, ChallengerSupernodeAccount: reporter, TicketId: ticket, TranscriptHash: transcript, ResultClass: class}
}
