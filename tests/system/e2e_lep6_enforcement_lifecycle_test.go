//go:build system_test

package system

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

// TestLEP6StorageTruthEnforcementLifecycle validates the storage-truth
// enforcement-state path on a real local lumerad chain. It deliberately uses
// deterministic storage-proof txs instead of opportunistic daemon timing so the
// test can prove the chain predicates directly:
//
//  1. a confirmed INDEX failure raises target node suspicion into postpone band;
//  2. epoch-end enforcement moves the target supernode to POSTPONED;
//  3. postponed target remains challengeable by active reporters;
//  4. clean PASS proofs reduce suspicion below watch and satisfy clean-pass recovery;
//  5. epoch-end enforcement recovers the supernode to ACTIVE.
func TestLEP6StorageTruthEnforcementLifecycle(t *testing.T) {
	os.Setenv("INTEGRATION_TEST", "true")
	defer os.Unsetenv("INTEGRATION_TEST")

	const (
		epochLengthBlocks = uint64(8)
		originHeight      = int64(1)
		ticketID          = "sys-test-ticket-soft-postpone"
	)

	t.Log("Storage-truth enforcement Step 1: start real chain with low enforcement thresholds")
	sut.ModifyGenesisJSON(t,
		SetStakingBondDenomUlume(t),
		SetActionParams(t),
		SetSupernodeMetricsParams(t),
		setSupernodeParamsForAuditTests(t),
		setAuditParamsForFastEpochs(t, epochLengthBlocks, 1, 1, 1, []uint32{4444}),
		setAuditMissingReportGraceForRuntimeE2E(t),
		// FULL mode submits RECENT+OLD INDEX HASH_MISMATCH results for two distinct
		// tickets. Together they contribute at least +104 node suspicion and also
		// satisfy the chain's distinct-ticket postpone predicate. Use production
		// decay semantics, compressed to local epochs, so recovery proves both clean
		// PASS credit and decay-adjusted suspicion falling below watch.
		setStorageTruthTestParams(t, "STORAGE_TRUTH_ENFORCEMENT_MODE_FULL", 104, 100, 1000, 920, 10),
		setStorageTruthEnforcementRecoveryParams(t, 1, 1),
	)
	sut.StartChain(t)
	cli := NewLumeradCLI(t, sut, true)

	n0 := getNodeIdentity(t, cli, "node0")
	n1 := getNodeIdentity(t, cli, "node1")
	n2 := getNodeIdentity(t, cli, "node2")
	nodes := []testNodeIdentity{n0, n1, n2}
	registerSupernode(t, cli, n0, "localhost:4444")
	registerSupernode(t, cli, n1, "localhost:4446")
	registerSupernode(t, cli, n2, "localhost:4448")
	bootstrapRuntimeSupernodeEligibility(t, cli)

	t.Log("Storage-truth enforcement Step 2: submit deterministic INDEX HASH_MISMATCH across two assigned epochs")
	failEpochID, failEpochEnd := nextUsableEpoch(t, originHeight, epochLengthBlocks)
	awaitCurrentEpochAnchorWithActiveSupernodes(t, failEpochID, n0.accAddr, n1.accAddr, n2.accAddr)
	target := selectAssignedStorageTruthTarget(t, failEpochID, nodes)
	seedProofTranscriptsWithClass(
		t,
		cli,
		failEpochID,
		nodes,
		target.accAddr,
		[]transcriptSeed{{ticketID: ticketID + "-a", transcriptHash: "storage-truth-index-hash-mismatch-a"}},
		true,
		"STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH",
	)
	awaitAtLeastHeight(t, failEpochEnd)
	sut.AwaitNextBlock(t)

	failEpochID, failEpochEnd = nextEpochWithAssignedStorageTruthTarget(t, originHeight, epochLengthBlocks, nodes, target.accAddr)
	seedProofTranscriptsWithClass(
		t,
		cli,
		failEpochID,
		nodes,
		target.accAddr,
		[]transcriptSeed{{ticketID: ticketID + "-b", transcriptHash: "storage-truth-index-hash-mismatch-b"}},
		true,
		"STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH",
	)

	nodeState, found := auditQueryNodeSuspicionStateST(t, target.accAddr)
	require.True(t, found, "HASH_MISMATCH must create node suspicion state for target")
	require.GreaterOrEqual(t, nodeState.SuspicionScore, int64(104), "target suspicion must reach postpone threshold")
	require.Equal(t, uint32(4), nodeState.ClassACountWindow, "FULL-mode INDEX HASH_MISMATCH must count RECENT+OLD Class-A failures for both tickets")
	require.Equal(t, uint32(0), nodeState.CleanPassCount, "failure must reset/avoid clean-pass recovery credit")

	t.Log("Storage-truth enforcement Step 3: epoch-end enforcement postpones the suspected target")
	awaitAtLeastHeight(t, failEpochEnd)
	sut.AwaitNextBlock(t)
	require.Eventually(t, func() bool {
		return querySupernodeLatestState(t, cli, target.valAddr) == "SUPERNODE_STATE_POSTPONED"
	}, 45*time.Second, time.Second, "target supernode must become POSTPONED after suspicion threshold crossing")

	t.Log("Storage-truth enforcement Step 4: submit clean PASS proofs while target is postponed")
	passCandidates := storageTruthCandidatesExcept(nodes, target.accAddr)
	passEpochID, passEpochEnd := nextEpochWithAssignedStorageTruthTarget(t, originHeight, epochLengthBlocks, passCandidates, target.accAddr)
	seedProofTranscriptsWithClass(
		t,
		cli,
		passEpochID,
		passCandidates,
		target.accAddr,
		[]transcriptSeed{{ticketID: ticketID + "-a", transcriptHash: "storage-truth-clean-pass-a"}},
		true,
		"STORAGE_PROOF_RESULT_CLASS_PASS",
	)

	recoveredScore, found := auditQueryNodeSuspicionStateST(t, target.accAddr)
	require.True(t, found)
	require.GreaterOrEqual(t, recoveredScore.CleanPassCount, uint32(1), "PASS pair must satisfy configured clean-pass recovery requirement")

	t.Log("Storage-truth enforcement Step 5: epoch-end enforcement recovers the clean target after decay-adjusted suspicion falls below watch")
	// Raw query state is only updated on score writes; enforcement applies the
	// chain-authoritative decay-adjusted read at epoch end. Advance enough fast
	// local epochs for score decay to move below the watch threshold, then assert
	// the postponed node recovers to ACTIVE.
	awaitAtLeastHeight(t, passEpochEnd+int64(epochLengthBlocks*6), 2*time.Minute)
	sut.AwaitNextBlock(t)
	require.Eventually(t, func() bool {
		return querySupernodeLatestState(t, cli, target.valAddr) == "SUPERNODE_STATE_ACTIVE"
	}, 45*time.Second, time.Second, "clean target must recover to ACTIVE after score and clean-pass predicates are satisfied")
}

func nextUsableEpoch(t *testing.T, originHeight int64, epochLengthBlocks uint64) (uint64, int64) {
	t.Helper()
	currentHeight := sut.AwaitNextBlock(t)
	epochID, epochStart := nextEpochAfterHeight(originHeight, epochLengthBlocks, currentHeight)
	awaitAtLeastHeight(t, epochStart)
	return epochID, epochStart + int64(epochLengthBlocks)
}

func selectAssignedStorageTruthTarget(t *testing.T, epochID uint64, candidates []testNodeIdentity) testNodeIdentity {
	t.Helper()
	byAccount := make(map[string]testNodeIdentity, len(candidates))
	for _, c := range candidates {
		byAccount[c.accAddr] = c
	}
	for _, reporter := range candidates {
		resp := auditQueryAssignedTargets(t, epochID, true, reporter.accAddr)
		for _, targetAcct := range resp.TargetSupernodeAccounts {
			if target, ok := byAccount[targetAcct]; ok && target.accAddr != reporter.accAddr {
				return target
			}
		}
	}
	require.Failf(t, "no assigned storage-truth target", "no candidate had an assigned target in epoch %d", epochID)
	return testNodeIdentity{}
}

func storageTruthCandidatesExcept(candidates []testNodeIdentity, account string) []testNodeIdentity {
	out := make([]testNodeIdentity, 0, len(candidates)-1)
	for _, c := range candidates {
		if c.accAddr != account {
			out = append(out, c)
		}
	}
	return out
}

func nextEpochWithAssignedStorageTruthTarget(t *testing.T, originHeight int64, epochLengthBlocks uint64, candidates []testNodeIdentity, targetAcct string) (uint64, int64) {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		epochID, epochEnd := nextUsableEpoch(t, originHeight, epochLengthBlocks)
		for _, reporter := range candidates {
			resp := auditQueryAssignedTargets(t, epochID, true, reporter.accAddr)
			for _, assigned := range resp.TargetSupernodeAccounts {
				if assigned == targetAcct {
					return epochID, epochEnd
				}
			}
		}
		awaitAtLeastHeight(t, epochEnd)
		sut.AwaitNextBlock(t)
	}
	require.Failf(t, "target not reassigned", "postponed target %s was not assigned to active reporters within 8 epochs", targetAcct)
	return 0, 0
}

func setStorageTruthEnforcementRecoveryParams(t *testing.T, cleanPasses, strongCleanPasses uint32) GenesisMutator {
	return func(genesis []byte) []byte {
		t.Helper()
		state, err := sjson.SetBytes(genesis, "app_state.audit.params.storage_truth_recovery_clean_pass_count", cleanPasses)
		require.NoError(t, err)
		state, err = sjson.SetBytes(state, "app_state.audit.params.storage_truth_strong_recovery_clean_pass_count", strongCleanPasses)
		require.NoError(t, err)
		return state
	}
}
