package storage_challenge

import (
	"context"
	"fmt"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/stretchr/testify/require"
)

// LEP-6 review regression: LEP-6 PR286 review fix regression tests.
//
// Coverage:
//   - L5: when NO_ELIGIBLE is emitted after selecting a ticket with no concrete
//     artifact universe, the selected ticket id must NOT leak into the chain
//     row's TicketId field (chain rejects).
//   - NO_ELIGIBLE row keeps ticket_id="" and artifact_class=UNSPECIFIED.
//
// Artifact-class fallback for one-class tickets is covered here at the dispatcher
// boundary and in pkg/storagechallenge/deterministic/lep6_test.go.
// H4/H5 invariants are covered by lep6_dispatch_test.go +
// result_buffer_test.go after the LEP-6 dispatcher rewrites; this file targets
// the selected-ticket/no-eligible row-shape regressions.

// TestDispatchEpoch_NoConcreteArtifactUniverseEmitsNoEligible_TicketIdEmpty
// exercises the selected-ticket NO_ELIGIBLE path: when a bucket has a selected
// ticket but the ticket has no concrete artifact universe, the dispatcher emits
// NO_ELIGIBLE_TICKET and keeps the chain row TicketId empty.
func TestDispatchEpoch_NoConcreteArtifactUniverseEmitsNoEligible_TicketIdEmpty(t *testing.T) {
	lep6metrics.Reset()
	t.Cleanup(lep6metrics.Reset)
	const epochID uint64 = 4242
	anchor := makeAnchor(epochID, 200, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	// This ticket has no chain-valid artifact universe in either class, so it must
	// remain NO_ELIGIBLE_TICKET.
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-timeout", AnchorBlock: 100}},
	}}
	// Under chain-canonical count resolution, len(RqIdsIds) is the fallback for
	// both classes. Keep the fallback universe empty here so the INDEX roll has
	// no chain-valid artifact universe and must emit NO_ELIGIBLE rather than
	// swapping classes.
	meta := stubMetaProvider{
		meta: &actiontypes.CascadeMetadata{
			RqIdsIc:  0,
			RqIdsMax: 1,
			RqIdsIds: []string{},
		},
		size: 4 * 1024,
	}
	d, buf := newDispatcher(t, audit, &stubFactory{}, tickets, meta)

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	results := buf.CollectResults(epochID)
	require.NotEmpty(t, results)

	var sawNoEligible bool
	for _, r := range results {
		if r.TargetSupernodeAccount == "sn-target" &&
			r.BucketType == audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT &&
			r.ResultClass == audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET {
			sawNoEligible = true
			// L5: chain validator (msg_submit_epoch_report_storage_proofs.go:92-94)
			// rejects NO_ELIGIBLE rows with non-empty ticket_id. Ensure we did
			// NOT leak the selected ticket id into the row.
			require.Equal(t, "", r.TicketId,
				"H6/L5: NO_ELIGIBLE row must keep ticket_id=\"\" (got %q)", r.TicketId)
			require.Equal(t, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED, r.ArtifactClass,
				"H6: NO_ELIGIBLE row must keep artifact_class=UNSPECIFIED")
			require.NotEmpty(t, r.ChallengerSignature,
				"H4: NO_ELIGIBLE row must carry a non-empty signature")
		}
	}
	require.True(t, sawNoEligible, "expected NO_ELIGIBLE_TICKET row in RECENT bucket")
	snap := lep6metrics.Snapshot()
	require.Equal(t, uint64(1), snap.NoEligibleReasonsTotal["reason=artifact_universe_empty,bucket=storage_proof_bucket_type_recent"])
	require.Contains(t, snap.RecentChallengeRefs, lep6metrics.ChallengeRef{ChallengeID: results[0].TranscriptHash, TimestampUnix: snap.RecentChallengeRefs[0].TimestampUnix, EpochID: epochID, ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET.String()})
	detail, ok := lep6metrics.GetChallenge(results[0].TranscriptHash)
	require.True(t, ok)
	require.Equal(t, noEligibleReasonArtifactUniverseEmpty, detail.FailureStage)
}

// TestDispatchEpoch_OneClassMissingFallsBackToOtherClass covers LEP-6 §10 at
// the dispatcher boundary: when the deterministic roll selects a missing SYMBOL
// class but INDEX exists, dispatch must challenge INDEX instead of emitting
// NO_ELIGIBLE_TICKET.
func TestDispatchEpoch_OneClassMissingFallsBackToOtherClass(t *testing.T) {
	const epochID uint64 = 4243
	anchor := makeAnchor(epochID, 200, "sn-target")
	params := defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)
	params.StorageTruthCompoundRangeLenBytes = 1
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: params},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	// `tkt-happy` rolls SYMBOL (verified). With SymbolArtifactCount=0 and
	// IndexArtifactCount=1, the dispatcher must fall back to INDEX and reach the
	// proof path.
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-happy", AnchorBlock: 100}},
	}}
	meta := stubMetaProvider{
		meta: &actiontypes.CascadeMetadata{
			Signatures:          "index-signature-format",
			RqIdsIc:             1,
			RqIdsMax:            1,
			IndexArtifactCount:  1,
			SymbolArtifactCount: 0,
			RqIdsIds:            []string{},
		},
		size: 4 * 1024,
	}
	targetClient := &stubCompoundClient{resp: makeOKCompoundResponse(t, 0, 1)}
	d, buf := newDispatcher(t, audit, &stubFactory{client: targetClient}, tickets, meta)

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	require.NotEmpty(t, targetClient.requests, "fallback must reach target proof RPC")
	require.Equal(t, uint32(audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX), targetClient.requests[0].ArtifactClass)
	require.NotEmpty(t, targetClient.requests[0].ArtifactKey)
	require.Equal(t, uint64(1), targetClient.requests[0].Ranges[0].End-targetClient.requests[0].Ranges[0].Start)

	results := buf.CollectResults(epochID)
	var sawPass bool
	for _, r := range results {
		if r.TicketId == "tkt-happy" {
			sawPass = true
			require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS, r.ResultClass)
			require.Equal(t, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, r.ArtifactClass)
			require.Equal(t, uint32(1), r.ArtifactCount)
		}
	}
	require.True(t, sawPass, "one-class fallback must produce a proof row, not NO_ELIGIBLE")
	require.Equal(t, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL,
		deterministic.SelectArtifactClass(anchor.Seed, "sn-target", "tkt-happy", 1, 1),
		"test setup must roll SYMBOL before the zero-count fallback is applied")
}

// TestBuffer_H5_DeterministicCrossChallenger pins H5's deterministic-tiebreak
// invariant: two challengers that observe entries in the same arrival order
// must produce identical drop decisions. Sequence number provides
// monotonicity even when wall-clock timestamps coincide on fast paths.
func TestBuffer_H5_DeterministicCrossChallenger(t *testing.T) {
	build := func() []string {
		b := NewBuffer()
		// 18 entries across 2 (target, bucket) groups in a fixed arrival order.
		for i := 0; i < 10; i++ {
			b.Append(123, mkResultForTarget(bucketRecent, fmt.Sprintf("rA-%02d", i), "tA"))
		}
		for i := 0; i < 8; i++ {
			b.Append(123, mkResultForTarget(bucketOld, fmt.Sprintf("oB-%02d", i), "tB"))
		}
		return ticketIDsOf(b.CollectResults(123))
	}
	a := build()
	b := build()
	require.Equal(t, a, b, "two runs with identical arrival order must produce identical kept set")
	require.Len(t, a, 16)
}
