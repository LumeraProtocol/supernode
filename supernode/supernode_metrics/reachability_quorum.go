package supernode_metrics

import (
	"context"
	"strings"
)

func (hm *Collector) silenceImpliesClosed(ctx context.Context) bool {
	if hm == nil || hm.lumeraClient == nil {
		return false
	}
	if ProbeAssignmentsPerEpoch <= 0 || ProbeQuorum <= 0 || ProbeAssignmentsPerEpoch < ProbeQuorum {
		return false
	}

	height, ok := hm.latestBlockHeight(ctx)
	if !ok {
		return false
	}

	epochBlocks := hm.probingEpochBlocks()
	if epochBlocks == 0 {
		return false
	}
	epochID := uint64(height) / epochBlocks
	epochStartHeight := int64(epochID * epochBlocks)

	selfIdentity := strings.TrimSpace(hm.identity)
	if selfIdentity == "" {
		return false
	}

	// Prefer the probing loop's epoch snapshot so quorum calculations match the
	// deterministic schedule used by the traffic generator. This avoids any subtle
	// mismatch if peers appear/disappear between list calls.
	if plan := hm.getProbePlan(); plan != nil &&
		plan.epochID == epochID &&
		plan.epochBlocks == epochBlocks &&
		len(plan.senders) > 0 &&
		len(plan.receivers) > 0 &&
		plan.peersByID != nil &&
		plan.assignedProbers != nil &&
		plan.expectedInbound != nil {
		return aliveAssignedProbersMeetsQuorum(selfIdentity, plan.peersByID, plan.assignedProbers, plan.expectedInbound, epochStartHeight, height, hm.metricsFreshnessMaxBlocks)
	}

	snModule := hm.lumeraClient.SuperNode()
	if snModule == nil {
		return false
	}

	resp, err := snModule.ListSuperNodes(ctx)
	if err != nil || resp == nil {
		return false
	}

	senders, receivers, peersByID := buildProbeCandidates(resp.Supernodes)
	if len(senders) == 0 || len(receivers) == 0 || peersByID == nil {
		return false
	}

	chainID, randBytes, _, ok := hm.epochAssignmentInputs(ctx, epochID, epochBlocks)
	if !ok {
		return false
	}
	asn := buildAssignment(chainID, epochID, randBytes, senders, receivers, ProbeAssignmentsPerEpoch)
	if asn == nil {
		return false
	}

	return aliveAssignedProbersMeetsQuorum(selfIdentity, peersByID, asn.byRecv, asn.expected, epochStartHeight, height, hm.metricsFreshnessMaxBlocks)
}

func isAliveProber(peer probeTarget, epochStartHeight int64, currentHeight int64, freshnessMaxBlocks uint64) bool {
	if peer.metricsHeight <= 0 {
		return false
	}
	if peer.metricsHeight < epochStartHeight {
		return false
	}
	if freshnessMaxBlocks == 0 {
		return true
	}
	if currentHeight < peer.metricsHeight {
		return true
	}
	return currentHeight-peer.metricsHeight <= int64(freshnessMaxBlocks)
}

func aliveAssignedProbersMeetsQuorum(
	selfID string,
	peersByID map[string]probeTarget,
	assignedProbers map[string][]string,
	expectedInbound map[string]int,
	epochStartHeight int64,
	currentHeight int64,
	freshnessMaxBlocks uint64,
) bool {
	if selfID == "" || peersByID == nil || assignedProbers == nil || expectedInbound == nil {
		return false
	}
	expected := expectedInbound[selfID]
	if expected < ProbeQuorum {
		return false
	}
	probers := assignedProbers[selfID]
	seen := map[string]struct{}{}
	alive := 0
	for _, proberID := range probers {
		if _, dup := seen[proberID]; dup {
			continue
		}
		seen[proberID] = struct{}{}
		if p, ok := peersByID[proberID]; ok {
			if isAliveProber(p, epochStartHeight, currentHeight, freshnessMaxBlocks) {
				alive++
			}
		}
	}
	if len(seen) < ProbeQuorum {
		return false
	}
	return alive >= ProbeQuorum
}
