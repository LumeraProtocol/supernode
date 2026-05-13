package recheck

import (
	"context"
	"errors"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

// TestAttestor_MultiTargetSameTicketBothPersist is the LEP-6 C2
// regression test Matee called out: two distinct targets within the same
// (epoch, ticket) must each produce a persisted dedup row and a chain
// submit. The previous PK collapsed both into one row and dropped the
// second submit.
func TestAttestor_MultiTargetSameTicketBothPersist(t *testing.T) {
	callSeq = 0
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{}
	a := NewAttestor("self", msg, store)

	mk := func(target string) Candidate {
		return Candidate{
			EpochID:                  7,
			TargetAccount:            target,
			TicketID:                 "ticket-1",
			ChallengedTranscriptHash: "orig-hash",
			OriginalReporter:         "reporter",
			OriginalResultClass:      audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH,
		}
	}
	result := RecheckResult{TranscriptHash: "recheck-hash", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS, Details: "ok"}

	require.NoError(t, a.Submit(ctx, mk("target-a"), result))
	require.NoError(t, a.Submit(ctx, mk("target-b"), result))

	// Both targets must be persisted in the dedup store.
	exA, err := store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-a")
	require.NoError(t, err)
	require.True(t, exA, "target-a must be persisted")
	exB, err := store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-b")
	require.NoError(t, err)
	require.True(t, exB, "target-b must be persisted (C2 regression)")

	// Both must have produced a chain submit.
	require.Len(t, msg.calls, 2)
	require.NotEqual(t, msg.calls[0].target, msg.calls[1].target)
}

// fakeReporterErrAudit is a stub that fails for "reporter-bad" and returns
// candidates for "reporter-good".
type fakeReporterErrAudit struct {
	stubAudit
}

func (f *fakeReporterErrAudit) GetEpochReportsByReporter(ctx context.Context, reporterAccount string, epochID uint64) (*audittypes.QueryEpochReportsByReporterResponse, error) {
	if reporterAccount == "reporter-bad" {
		return nil, errors.New("rpc unavailable")
	}
	if epochID != f.current {
		return &audittypes.QueryEpochReportsByReporterResponse{}, nil
	}
	report := audittypes.EpochReport{
		StorageProofResults: []*audittypes.StorageProofResult{
			{
				ChallengerSupernodeAccount: "reporter-good",
				TargetSupernodeAccount:     "target",
				TicketId:                   "ticket-good",
				TranscriptHash:             "h",
				ResultClass:                audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH,
			},
		},
	}
	return &audittypes.QueryEpochReportsByReporterResponse{Reports: []audittypes.EpochReport{report}}, nil
}

// staticTwoReporters returns both reporters so the finder iterates them.
type staticTwoReporters struct{}

func (staticTwoReporters) ReporterAccounts(_ context.Context) ([]string, error) {
	return []string{"reporter-bad", "reporter-good"}, nil
}

// TestFinder_PerReporterErrorIsolation is the LEP-6 L4 regression: a
// single failing reporter RPC must NOT mask candidates from other
// reporters.
func TestFinder_PerReporterErrorIsolation(t *testing.T) {
	ctx := context.Background()
	a := &fakeReporterErrAudit{stubAudit: stubAudit{current: 5, mode: audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL}}
	store := newMemoryStore()

	f := NewFinderWithReporters(a, store, "self", FinderConfig{LookbackEpochs: 1, MaxPerTick: 10}, staticTwoReporters{})
	out, err := f.Find(ctx)
	require.NoError(t, err, "per-reporter error must not propagate")
	require.Len(t, out, 1)
	require.Equal(t, "reporter-good", out[0].OriginalReporter)
	require.Equal(t, "ticket-good", out[0].TicketID)
}
