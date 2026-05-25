package recheck

import (
	"context"
	"errors"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

// TestAttestor_MultiTargetSameTicketCollapsesToOne is the PR286 F3
// regression: chain replay protection is per (epoch, ticket, creator),
// NOT target. Two distinct target candidates within the same
// (epoch, ticket) submitted by the same local creator must collapse —
// the first lands, the second is rejected by the local dedup (mirroring
// what chain would do) and produces no second chain submit.
//
// This replaces the pre-F3 TestAttestor_MultiTargetSameTicketBothPersist,
// which asserted the buggy per-target behavior.
func TestAttestor_MultiTargetSameTicketCollapsesToOne(t *testing.T) {
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

	// First target lands; second target on same (epoch, ticket) collides
	// with the existing local dedup row. The attestor treats the
	// ErrLEP6RecheckAlreadyRecorded surface as "another tick already
	// handled this candidate", emits a stage_dedup metric, and returns
	// nil — no second chain submit.
	require.NoError(t, a.Submit(ctx, mk("target-a"), result))
	require.NoError(t, a.Submit(ctx, mk("target-b"), result), "second target must be a no-op via stage_dedup")

	// Local dedup row exists; checking via either target returns true
	// because the key is (epoch, ticket).
	ex, err := store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-a")
	require.NoError(t, err)
	require.True(t, ex, "(epoch, ticket) row must be persisted")
	ex, err = store.HasRecheckSubmission(ctx, 7, "ticket-1", "target-b")
	require.NoError(t, err)
	require.True(t, ex, "HasRecheckSubmission must read positive for any target on same (epoch, ticket) — chain replay key is per (epoch, ticket, creator)")

	// Exactly one chain submit fired — the first one.
	require.Len(t, msg.calls, 1, "PR286 F3: chain accepts one recheck per (epoch, ticket, creator); only one local submit must fire")
	require.Equal(t, "target-a", msg.calls[0].target)
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
