package storage_challenge

import (
	"context"
	"fmt"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

// Wave 3 — LEP-6 PR286 review fix regression tests.
//
// Coverage:
//   - H6: SelectArtifactClass with empty rolled class emits NO_ELIGIBLE_TICKET
//     (no class swap). Verified end-to-end through DispatchEpoch:
//        * INDEX-only ticket where the class roll lands on SYMBOL → NO_ELIGIBLE.
//        * SYMBOL-only ticket where the class roll lands on INDEX  → NO_ELIGIBLE.
//        * NO_ELIGIBLE row keeps ticket_id="" (chain validator requirement).
//   - L5: when NO_ELIGIBLE is emitted post-class-roll, the selected ticket id
//     must NOT leak into the chain row's TicketId field (chain rejects).
//
// H4/H5 invariants are covered by lep6_dispatch_test.go +
// result_buffer_test.go after the wave-3 rewrites; this file targets the
// behavioural regressions specific to H6/L5 that did not have a direct test
// before this wave.

// TestDispatchEpoch_H6_NoSwapEmitsNoEligible_TicketIdEmpty exercises the
// post-Wave-3 SelectArtifactClass behavior: with `tkt-T0` (rolls SYMBOL when
// both classes are present) and indexCount=0, the function must return
// UNSPECIFIED — wait, that's only the indexCount=0 + INDEX-roll case. For
// SYMBOL-roll + indexCount=0 we still return SYMBOL and the dispatcher hits
// the meta validation. So instead we use a ticket id that rolls INDEX with
// indexCount=0 → UNSPECIFIED → NO_ELIGIBLE.
func TestDispatchEpoch_H6_RollEmptyEmitsNoEligibleNotSwap(t *testing.T) {
	const epochID uint64 = 4242
	anchor := makeAnchor(epochID, 200, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	// `tkt-timeout` rolls INDEX under makeAnchor's seed (verified empirically;
	// see find_symbol_roll.go probe). With indexCount=0 the post-Wave-3
	// behaviour MUST be UNSPECIFIED → NO_ELIGIBLE_TICKET. Pre-Wave-3 code
	// would have swapped to SYMBOL and tried to dispatch.
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-timeout", AnchorBlock: 100}},
	}}
	// IndexArtifactCount derives from RqIdsIc, SymbolArtifactCount from len(RqIdsIds).
	// RqIdsIc=0 → INDEX class empty.
	meta := stubMetaProvider{
		meta: &actiontypes.CascadeMetadata{
			RqIdsIc:  0,
			RqIdsMax: 1,
			RqIdsIds: []string{"sym-0"}, // SYMBOL count = 1
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
}

// TestDispatchEpoch_H6_SymbolEmptyAlsoEmitsNoEligible covers the inverse case:
// SYMBOL-rolled ticket where SymbolArtifactCount=0 must emit NO_ELIGIBLE.
func TestDispatchEpoch_H6_SymbolEmptyEmitsNoEligible(t *testing.T) {
	const epochID uint64 = 4243
	anchor := makeAnchor(epochID, 200, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	// `tkt-happy` rolls SYMBOL (verified). With SymbolArtifactCount=0 the
	// dispatcher must emit NO_ELIGIBLE rather than swapping to INDEX.
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-happy", AnchorBlock: 100}},
	}}
	meta := stubMetaProvider{
		meta: &actiontypes.CascadeMetadata{
			RqIdsIc:  3, // INDEX class non-empty
			RqIdsMax: 1,
			RqIdsIds: []string{}, // SYMBOL count = 0
		},
		size: 4 * 1024,
	}
	d, buf := newDispatcher(t, audit, &stubFactory{}, tickets, meta)

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	results := buf.CollectResults(epochID)
	require.NotEmpty(t, results)

	var sawNoEligibleRecent bool
	for _, r := range results {
		if r.BucketType == audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT &&
			r.ResultClass == audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET {
			sawNoEligibleRecent = true
			require.Equal(t, "", r.TicketId)
			require.Equal(t, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED, r.ArtifactClass)
		}
	}
	require.True(t, sawNoEligibleRecent, "SYMBOL-empty + SYMBOL-roll must emit NO_ELIGIBLE")
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
