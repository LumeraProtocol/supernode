package deterministic

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/btcsuite/btcutil/base58"
	"lukechampine.com/blake3"
)

type xorCandidate struct {
	id   string
	dist [32]byte
}

func EpochID(height int64, epochZeroHeight uint64, epochLengthBlocks uint64) (uint64, bool) {
	if epochLengthBlocks == 0 {
		return 0, false
	}
	if height < int64(epochZeroHeight) {
		return 0, false
	}
	return uint64(height-int64(epochZeroHeight)) / epochLengthBlocks, true
}

func EpochStartHeight(epochID uint64, epochZeroHeight uint64, epochLengthBlocks uint64) int64 {
	return int64(epochZeroHeight) + int64(epochID)*int64(epochLengthBlocks)
}

func ComparisonTargetForChallengers(seed []byte, epochID uint64) string {
	return "sc:challengers:" + hex.EncodeToString(seed) + ":" + strconv.FormatUint(epochID, 10)
}

func ChallengerCount(nActive int, requested uint32) int {
	if nActive <= 0 {
		return 0
	}
	if requested == 0 {
		// auto = ceil(N/3), minimum 1
		return maxInt(1, (nActive+2)/3)
	}
	if int(requested) > nActive {
		return nActive
	}
	return int(requested)
}

func SelectTopByXORDistance(ids []string, target []byte, k int) []string {
	if k <= 0 || len(ids) == 0 {
		return nil
	}
	targetHash := ensureHashedTarget(target)

	candidates := make([]xorCandidate, 0, len(ids))
	for _, id := range ids {
		idHash := blake3.Sum256([]byte(id))
		var dist [32]byte
		for i := 0; i < 32; i++ {
			dist[i] = idHash[i] ^ targetHash[i]
		}
		candidates = append(candidates, xorCandidate{id: id, dist: dist})
	}

	sort.Slice(candidates, func(i, j int) bool {
		cmp := bytes.Compare(candidates[i].dist[:], candidates[j].dist[:])
		if cmp != 0 {
			return cmp < 0
		}
		return candidates[i].id < candidates[j].id
	})

	if k > len(candidates) {
		k = len(candidates)
	}
	out := make([]string, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, candidates[i].id)
	}
	return out
}

func SelectChallengers(activeIDs []string, seed []byte, epochID uint64, requested uint32) []string {
	k := ChallengerCount(len(activeIDs), requested)
	target := ComparisonTargetForChallengers(seed, epochID)
	return SelectTopByXORDistance(activeIDs, []byte(target), k)
}

func SelectReplicaSet(activeIDs []string, fileKeyBase58 string, replicaCount uint32) ([]string, error) {
	if replicaCount == 0 {
		return nil, nil
	}
	target := base58.Decode(fileKeyBase58)
	if len(target) == 0 {
		return nil, fmt.Errorf("invalid base58 file_key")
	}
	k := int(replicaCount)
	return SelectTopByXORDistance(activeIDs, target, k), nil
}

func SelectFileKeys(candidateKeys []string, seed []byte, epochID uint64, challengerID string, count uint32) []string {
	if count == 0 || len(candidateKeys) == 0 {
		return nil
	}

	keys := append([]string(nil), candidateKeys...)
	sort.Strings(keys)

	want := int(count)
	if want > len(keys) {
		want = len(keys)
	}

	out := make([]string, 0, want)
	seedHex := hex.EncodeToString(seed)

	for i := 0; i < want; i++ {
		msg := []byte("sc:files:" + challengerID + ":" + strconv.FormatUint(epochID, 10) + ":" + seedHex + ":" + strconv.Itoa(i))
		sum := blake3.Sum256(msg)
		idx := int(binary.BigEndian.Uint64(sum[0:8]) % uint64(len(keys)))
		out = append(out, keys[idx])

		// remove selected key (stable, deterministic)
		keys = append(keys[:idx], keys[idx+1:]...)
		if len(keys) == 0 {
			break
		}
	}

	return out
}

func DeterministicJitterMs(seed []byte, epochID uint64, challengerID string, maxJitterMs uint64) uint64 {
	if maxJitterMs == 0 {
		return 0
	}
	msg := []byte("sc:jitter:" + hex.EncodeToString(seed) + ":" + strconv.FormatUint(epochID, 10) + ":" + challengerID)
	sum := blake3.Sum256(msg)
	v := binary.BigEndian.Uint64(sum[0:8])
	return v % maxJitterMs
}

func ensureHashedTarget(target []byte) [32]byte {
	if len(target) == 32 {
		var out [32]byte
		copy(out[:], target)
		return out
	}
	return blake3.Sum256(target)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
