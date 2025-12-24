package supernode_metrics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

const reachabilitySeedPrefixV2 = "lumera.reachability.v2|"

func reachabilitySeed(chainID string, epochID uint64, chainRandomness []byte) [32]byte {
	b := make([]byte, 0, len(reachabilitySeedPrefixV2)+len(chainID)+1+20+1+hex.EncodedLen(len(chainRandomness)))
	b = append(b, reachabilitySeedPrefixV2...)
	b = append(b, chainID...)
	b = append(b, '|')
	b = append(b, strconv.FormatUint(epochID, 10)...)
	b = append(b, '|')
	if len(chainRandomness) > 0 {
		dst := make([]byte, hex.EncodedLen(len(chainRandomness)))
		hex.Encode(dst, chainRandomness)
		b = append(b, dst...)
	}
	return sha256.Sum256(b)
}

type hashedTarget struct {
	t probeTarget
	h [32]byte
}

func permuteTargets(in []probeTarget, seed [32]byte) []probeTarget {
	if len(in) == 0 {
		return nil
	}
	tmp := make([]hashedTarget, 0, len(in))
	for _, t := range in {
		msg := make([]byte, 0, len(seed)+1+len(t.identity))
		msg = append(msg, seed[:]...)
		msg = append(msg, '|')
		msg = append(msg, t.identity...)
		tmp = append(tmp, hashedTarget{t: t, h: sha256.Sum256(msg)})
	}
	sort.Slice(tmp, func(i, j int) bool {
		return bytes.Compare(tmp[i].h[:], tmp[j].h[:]) < 0
	})
	out := make([]probeTarget, 0, len(tmp))
	for _, x := range tmp {
		out = append(out, x.t)
	}
	return out
}

type reachabilityAssignment struct {
	bySender  map[string][]string
	byRecv    map[string][]string
	expected  map[string]int
	seed      [32]byte
	permA     []probeTarget
	permR     []probeTarget
	epochID   uint64
	chainID   string
	randomHex string
}

func buildAssignment(chainID string, epochID uint64, chainRandomness []byte, senders []probeTarget, receivers []probeTarget, k int) *reachabilityAssignment {
	asn := &reachabilityAssignment{
		bySender: make(map[string][]string),
		byRecv:   make(map[string][]string),
		expected: make(map[string]int),
		epochID:  epochID,
		chainID:  chainID,
	}
	if len(chainRandomness) > 0 {
		asn.randomHex = hex.EncodeToString(chainRandomness)
	}

	if k <= 0 || len(senders) == 0 || len(receivers) == 0 {
		return asn
	}

	asn.seed = reachabilitySeed(chainID, epochID, chainRandomness)
	asn.permA = permuteTargets(senders, asn.seed)
	asn.permR = permuteTargets(receivers, asn.seed)
	if len(asn.permR) == 0 {
		return asn
	}

	for i, sender := range asn.permA {
		for j := 0; j < k; j++ {
			base := (i*k + j) % len(asn.permR)
			targetID := pickTargetAvoidSelf(sender.identity, base, asn.permR)
			if targetID == "" {
				continue
			}
			asn.bySender[sender.identity] = append(asn.bySender[sender.identity], targetID)
			asn.byRecv[targetID] = append(asn.byRecv[targetID], sender.identity)
			asn.expected[targetID]++
		}
	}

	return asn
}

func pickTargetAvoidSelf(senderID string, start int, permR []probeTarget) string {
	if len(permR) == 0 {
		return ""
	}
	for step := 0; step < len(permR); step++ {
		t := permR[(start+step)%len(permR)]
		if t.identity != senderID {
			return t.identity
		}
	}
	return ""
}
