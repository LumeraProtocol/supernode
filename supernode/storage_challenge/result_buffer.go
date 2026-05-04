package storage_challenge

import (
	"context"
	"sort"
	"sync"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
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
// past the cap, CollectResults applies a deterministic self-throttle: drop
// non-RECENT bucket entries first (oldest by ticket_id lex), then drop oldest
// RECENT entries by the same order, until the slice fits.
//
// Note: audittypes.StorageProofResult has no EpochId field; the challenger
// supplies the binding epoch at Append time so the buffer can drain only the
// relevant epoch and leave entries for other epochs intact.
//
// Buffer is safe for concurrent use.
type Buffer struct {
	mu      sync.Mutex
	byEpoch map[uint64][]*audittypes.StorageProofResult
}

// NewBuffer returns an empty Buffer.
func NewBuffer() *Buffer {
	return &Buffer{byEpoch: make(map[uint64][]*audittypes.StorageProofResult)}
}

// Append stores result under epochID. Nil results are ignored.
func (b *Buffer) Append(epochID uint64, result *audittypes.StorageProofResult) {
	if result == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.byEpoch[epochID] = append(b.byEpoch[epochID], result)
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

	// Make a defensive copy so we don't aliase caller data when we sort.
	out := make([]*audittypes.StorageProofResult, len(matching))
	copy(out, matching)

	const maxKeep = storagechallenge.MaxStorageProofResultsPerReport

	if len(out) > maxKeep {
		out = throttleResults(epochID, out, maxKeep)
	}

	sortDeterministic(out)
	return out
}

// throttleResults enforces len(results) <= maxKeep by:
//  1. Dropping oldest non-RECENT entries by ticket_id lex.
//  2. If still over cap (only RECENT remain), dropping oldest RECENT by same lex.
//
// All results in this call are bound to the same epochID, so the
// (epoch_id asc, ticket_id asc) lex specified in the LEP-6 plan collapses to
// ticket_id asc here. Kept for forward compatibility if the buffer ever
// throttles across epochs.
//
// A Warn log is emitted when throttling activates.
func throttleResults(epochID uint64, results []*audittypes.StorageProofResult, maxKeep int) []*audittypes.StorageProofResult {
	originalCount := len(results)

	recent := make([]*audittypes.StorageProofResult, 0, len(results))
	nonRecent := make([]*audittypes.StorageProofResult, 0, len(results))
	for _, r := range results {
		if r == nil {
			continue
		}
		if r.BucketType == audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT {
			recent = append(recent, r)
		} else {
			nonRecent = append(nonRecent, r)
		}
	}

	// Sort each partition oldest-first (ticket_id asc) so dropping from index 0
	// drops oldest.
	sort.SliceStable(nonRecent, func(i, j int) bool { return nonRecent[i].TicketId < nonRecent[j].TicketId })
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].TicketId < recent[j].TicketId })

	total := len(recent) + len(nonRecent)
	for total > maxKeep && len(nonRecent) > 0 {
		nonRecent = nonRecent[1:]
		total--
	}
	for total > maxKeep && len(recent) > 0 {
		recent = recent[1:]
		total--
	}

	kept := make([]*audittypes.StorageProofResult, 0, total)
	kept = append(kept, recent...)
	kept = append(kept, nonRecent...)

	logtrace.Warn(context.Background(), "storage_challenge: result buffer throttled to chain cap", logtrace.Fields{
		"epoch_id": epochID,
		"original": originalCount,
		"kept":     len(kept),
		"dropped":  originalCount - len(kept),
		"cap":      maxKeep,
		"policy":   "drop-non-RECENT-first",
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
