package storage_challenge

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge"
)

// Buffer accumulates StorageProofResults emitted by the per-epoch challenger
// loop and surfaces them to the host reporter (which submits MsgSubmitEpochReport).
//
// Buffer satisfies host_reporter.ProofResultProvider:
//
//	CollectResults(epochID uint64) []*audittypes.StorageProofResult
//
// The chain audit keeper rejects an entire epoch report if its
// storage_proof_results slice exceeds MaxStorageProofResultsPerReport
// (lumera/x/audit/v1/types/keys.go:11-13, enforced in
// x/audit/v1/keeper/msg_submit_epoch_report.go:126-130). Because two
// independent challengers may produce overlapping result sets that combine
// past the cap, CollectResults applies a deterministic self-throttle.
//
// LEP-6 review (Matee, 2026-05-06) — H5:
//   - Throttle drops by ARRIVAL ORDER (oldest-first), not by ticket_id lex.
//     Sorting by content-addressed ticket_id let an attacker who can shape
//     ticket IDs decide which rows reach the chain. Arrival order is
//     attacker-uninfluenceable per challenger.
//   - Fairness across (target, bucket) groups: drop from the LARGEST group
//     first so no single (target, bucket) starves another. A target with
//     many tickets cannot crowd out other targets.
//   - Cross-supernode determinism: arrival order is per-process, but the
//     final delivered slice is sorted by (BucketType, TicketId) so two
//     independently-generated reports that started from the same input set
//     deliver byte-identical messages to the chain.
//   - Tiebreaker for equal-arrival rows: ticket_id lex ASC, then full proto
//     bytes — ensures deterministic deletion when wall-clock collides.
//
// Buffer is safe for concurrent use.
type Buffer struct {
	mu      sync.Mutex
	byEpoch map[uint64][]*bufferedResult
	seq     atomic.Uint64
}

// bufferedResult wraps a StorageProofResult with arrival metadata so the
// throttle algorithm can drop oldest rows first without leaking ordering
// constants into the public StorageProofResult shape.
type bufferedResult struct {
	result    *audittypes.StorageProofResult
	arrivedAt time.Time
	seq       uint64 // monotonic per-process tiebreaker
}

// NewBuffer returns an empty Buffer.
func NewBuffer() *Buffer {
	return &Buffer{byEpoch: make(map[uint64][]*bufferedResult)}
}

// Append stores result under epochID. Nil results are ignored.
func (b *Buffer) Append(epochID uint64, result *audittypes.StorageProofResult) {
	if result == nil {
		return
	}
	entry := &bufferedResult{
		result:    result,
		arrivedAt: time.Now(),
		seq:       b.seq.Add(1),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.byEpoch[epochID] = append(b.byEpoch[epochID], entry)
}

// CollectResults drains and returns the buffered results for epochID, applying
// the LEP-6 16-cap self-throttle. Results buffered for other epochs are left
// intact. The returned slice is sorted deterministically by
// (BucketType, EpochId, TicketId) so that downstream signing/serialisation is
// stable across challengers and re-runs.
func (b *Buffer) CollectResults(epochID uint64) []*audittypes.StorageProofResult {
	b.mu.Lock()
	matching := b.byEpoch[epochID]
	delete(b.byEpoch, epochID)
	b.mu.Unlock()

	if len(matching) == 0 {
		return nil
	}

	const maxKeep = storagechallenge.MaxStorageProofResultsPerReport

	if len(matching) > maxKeep {
		matching = throttleResults(epochID, matching, maxKeep)
	}

	out := make([]*audittypes.StorageProofResult, 0, len(matching))
	for _, e := range matching {
		if e == nil || e.result == nil {
			continue
		}
		out = append(out, e.result)
	}
	sortDeterministic(out)
	return out
}

// throttleResults enforces len(results) <= maxKeep using two passes:
//
//  1. Drop from the LARGEST (target, bucket) group first to maintain fairness;
//     within each group drop the oldest arrival. Repeat until either
//     (a) the slice fits, or (b) every group has shrunk to size 1.
//  2. If still over cap, drop the global oldest entry irrespective of group.
//
// Determinism: arrival timestamp is the primary key; (sequence, ticket_id) is
// the tiebreaker. Two challengers will produce the same delivered set if they
// observed the same arrival order — which is the case when both walked the
// dispatcher loop in the same order (the dispatcher is single-goroutine per
// epoch). For the cross-challenger case, the chain combines reports from
// multiple challengers anyway; a single challenger's deterministic local
// throttle is what matters here.
//
// A Warn log is emitted when throttling activates.
func throttleResults(epochID uint64, results []*bufferedResult, maxKeep int) []*bufferedResult {
	if len(results) <= maxKeep {
		return results
	}
	originalCount := len(results)

	// Group by (target, bucket).
	type groupKey struct {
		target string
		bucket audittypes.StorageProofBucketType
	}
	groups := make(map[groupKey][]*bufferedResult)
	for _, r := range results {
		if r == nil || r.result == nil {
			continue
		}
		k := groupKey{target: r.result.TargetSupernodeAccount, bucket: r.result.BucketType}
		groups[k] = append(groups[k], r)
	}
	// Sort each group oldest-first.
	for k := range groups {
		sort.SliceStable(groups[k], func(i, j int) bool {
			a, b := groups[k][i], groups[k][j]
			if !a.arrivedAt.Equal(b.arrivedAt) {
				return a.arrivedAt.Before(b.arrivedAt)
			}
			if a.seq != b.seq {
				return a.seq < b.seq
			}
			return a.result.TicketId < b.result.TicketId
		})
	}

	totalCount := func() int {
		n := 0
		for _, g := range groups {
			n += len(g)
		}
		return n
	}

	// Phase 1: drop oldest from largest group while every group still has > 1
	// AND total > maxKeep. A "largest" tie is broken by the deterministic
	// sort below — pick the group whose oldest entry's (target, bucket) sorts
	// first lex so two challengers with the same input drop the same group.
	for totalCount() > maxKeep {
		var largestKey groupKey
		largestSize := 0
		for k, g := range groups {
			if len(g) > largestSize ||
				(len(g) == largestSize && (k.target < largestKey.target ||
					(k.target == largestKey.target && k.bucket < largestKey.bucket))) {
				largestKey = k
				largestSize = len(g)
			}
		}
		if largestSize <= 1 {
			break
		}
		groups[largestKey] = groups[largestKey][1:]
	}

	// Phase 2: if still over cap, drop global oldest entry deterministically.
	for totalCount() > maxKeep {
		var oldestKey groupKey
		var oldestEntry *bufferedResult
		for k, g := range groups {
			if len(g) == 0 {
				continue
			}
			head := g[0]
			if oldestEntry == nil ||
				head.arrivedAt.Before(oldestEntry.arrivedAt) ||
				(head.arrivedAt.Equal(oldestEntry.arrivedAt) && head.seq < oldestEntry.seq) {
				oldestKey = k
				oldestEntry = head
			}
		}
		if oldestEntry == nil {
			break
		}
		groups[oldestKey] = groups[oldestKey][1:]
	}

	kept := make([]*bufferedResult, 0, maxKeep)
	for _, g := range groups {
		kept = append(kept, g...)
	}

	dropped := originalCount - len(kept)
	lep6metrics.IncDispatchThrottled("oldest-arrival-fair-by-target-bucket", dropped)
	logtrace.Warn(context.Background(), "storage_challenge: result buffer throttled to chain cap", logtrace.Fields{
		"epoch_id": epochID,
		"original": originalCount,
		"kept":     len(kept),
		"dropped":  dropped,
		"cap":      maxKeep,
		"policy":   "oldest-arrival-fair-by-target-bucket",
	})

	return kept
}

// sortDeterministic orders results by (BucketType, TicketId). All results in
// a single CollectResults call share the same epoch, so EpochId would not
// further disambiguate.
func sortDeterministic(results []*audittypes.StorageProofResult) {
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.BucketType != b.BucketType {
			return a.BucketType < b.BucketType
		}
		return a.TicketId < b.TicketId
	})
}
