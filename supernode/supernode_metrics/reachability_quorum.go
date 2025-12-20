package supernode_metrics

import (
	"context"
	"os"
	"sort"
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

	peers := []probeTarget(nil)
	if plan := hm.getProbePlan(); plan != nil && plan.epochID == epochID && plan.epochBlocks == epochBlocks && len(plan.eligiblePeers) > 0 {
		// Use the probing loop's epoch snapshot so quorum calculations match
		// the deterministic schedule used by the traffic generator.
		peers = plan.eligiblePeers
	} else {
		snModule := hm.lumeraClient.SuperNode()
		if snModule == nil {
			return false
		}

		resp, err := snModule.ListSuperNodes(ctx)
		if err != nil || resp == nil {
			return false
		}

		isTest := strings.EqualFold(strings.TrimSpace(os.Getenv("INTEGRATION_TEST")), "true")
		peers = buildEligiblePeers(resp.Supernodes, isTest)
		sort.Slice(peers, func(i, j int) bool {
			return peers[i].identity < peers[j].identity
		})
	}
	if len(peers) < ProbeQuorum+1 {
		return false
	}

	selfIdentity := strings.TrimSpace(hm.identity)
	if selfIdentity == "" {
		return false
	}

	selfIndex := -1
	for i := range peers {
		if peers[i].identity == selfIdentity {
			selfIndex = i
			break
		}
	}
	if selfIndex < 0 {
		return false
	}

	offsets := deriveProbeOffsets(epochID, ProbeAssignmentsPerEpoch, len(peers))
	if len(offsets) < ProbeQuorum {
		return false
	}

	alive := 0
	for _, off := range offsets {
		idx := (selfIndex - off) % len(peers)
		if idx < 0 {
			idx += len(peers)
		}
		if isAliveProber(peers[idx], epochStartHeight, height, hm.metricsFreshnessMaxBlocks) {
			alive++
		}
	}

	return alive >= ProbeQuorum
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
