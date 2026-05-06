package storage_challenge

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

const (
	bucketRecent = audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT
	bucketOld    = audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD
)

func mkResult(bucket audittypes.StorageProofBucketType, ticket string) *audittypes.StorageProofResult {
	return &audittypes.StorageProofResult{
		TicketId:   ticket,
		BucketType: bucket,
	}
}

func mkResultForTarget(bucket audittypes.StorageProofBucketType, ticket, target string) *audittypes.StorageProofResult {
	return &audittypes.StorageProofResult{
		TicketId:               ticket,
		BucketType:             bucket,
		TargetSupernodeAccount: target,
	}
}

// ticketIDsOf extracts ticket IDs in slice order.
func ticketIDsOf(rs []*audittypes.StorageProofResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.TicketId
	}
	return out
}

func TestBuffer_BelowCap_ReturnsAllSortedDeterministically(t *testing.T) {
	b := NewBuffer()
	// Append in scrambled order; expect sort by (BucketType, TicketId).
	inputs := []*audittypes.StorageProofResult{
		mkResult(bucketOld, "t-old-b"),
		mkResult(bucketRecent, "t-recent-c"),
		mkResult(bucketRecent, "t-recent-a"),
		mkResult(bucketOld, "t-old-a"),
	}
	for _, r := range inputs {
		b.Append(5, r)
	}
	got := b.CollectResults(5)
	if len(got) != 4 {
		t.Fatalf("want 4 results, got %d", len(got))
	}
	// RECENT (=1) sorts before OLD (=2) because lower numeric enum.
	want := []string{"t-recent-a", "t-recent-c", "t-old-a", "t-old-b"}
	if !reflect.DeepEqual(ticketIDsOf(got), want) {
		t.Fatalf("ordering mismatch:\n  got:  %v\n  want: %v", ticketIDsOf(got), want)
	}
	// Buffer drained for epoch 5.
	if got2 := b.CollectResults(5); len(got2) != 0 {
		t.Fatalf("expected drained buffer, got %d results", len(got2))
	}
}

func TestBuffer_AboveCap_DropsByArrivalAndFairness(t *testing.T) {
	// LEP-6 review H5 (Matee): drop policy is now arrival-order with
	// (target, bucket) fairness, not "non-RECENT first by ticket_id lex".
	// 10 RECENT (target A) + 8 OLD (target B) = 18, cap 16 → 2 drops.
	// Both groups have size > 1 so phase-1 fairness drops one from the
	// LARGEST group first, then re-evaluates. Group A starts at 10, group B
	// at 8 → first drop is A's oldest (the very first appended). Now A=9,
	// B=8 → next drop is A's oldest again. Result: A=8, B=8.
	b := NewBuffer()
	for i := 0; i < 10; i++ {
		b.Append(7, mkResultForTarget(bucketRecent, fmt.Sprintf("recent-%02d", i), "tA"))
	}
	for i := 0; i < 8; i++ {
		b.Append(7, mkResultForTarget(bucketOld, fmt.Sprintf("old-%02d", i), "tB"))
	}
	got := b.CollectResults(7)
	if len(got) != 16 {
		t.Fatalf("want 16 results, got %d", len(got))
	}
	var nRecent, nOld int
	for _, r := range got {
		switch r.BucketType {
		case bucketRecent:
			nRecent++
		case bucketOld:
			nOld++
		}
	}
	// Fairness: largest group (RECENT/tA, 10) drops 2 oldest; OLD/tB (8) untouched.
	if nRecent != 8 || nOld != 8 {
		t.Fatalf("want 8 RECENT + 8 OLD (fairness), got %d RECENT + %d OLD", nRecent, nOld)
	}
	// The two oldest in the dropped group are recent-00 and recent-01.
	for _, r := range got {
		if r.TicketId == "recent-00" || r.TicketId == "recent-01" {
			t.Fatalf("expected oldest entries from largest group dropped; %q present", r.TicketId)
		}
	}
}

func TestBuffer_AboveCap_OnlyRecent_DropsOldestArrival(t *testing.T) {
	// LEP-6 review H5: 20 RECENT all same (target,bucket) group → fairness
	// phase drops oldest 4 by ARRIVAL ORDER. (Within a single group, arrival
	// order is the only key — deterministic per challenger.)
	b := NewBuffer()
	for i := 0; i < 20; i++ {
		b.Append(9, mkResultForTarget(bucketRecent, fmt.Sprintf("r-%02d", i), "t1"))
	}
	got := b.CollectResults(9)
	if len(got) != 16 {
		t.Fatalf("want 16 results, got %d", len(got))
	}
	// r-00..r-03 are oldest arrivals → dropped. r-04..r-19 survive.
	// Final delivered slice is sorted by (Bucket, TicketId) → ASC ticket id.
	want := []string{
		"r-04", "r-05", "r-06", "r-07", "r-08", "r-09",
		"r-10", "r-11", "r-12", "r-13", "r-14", "r-15",
		"r-16", "r-17", "r-18", "r-19",
	}
	if !reflect.DeepEqual(ticketIDsOf(got), want) {
		t.Fatalf("ordering mismatch:\n  got:  %v\n  want: %v", ticketIDsOf(got), want)
	}
}

func TestBuffer_DeterministicSorting(t *testing.T) {
	build := func() []*audittypes.StorageProofResult {
		b := NewBuffer()
		// Mix and match in a deliberately scrambled order.
		seqs := []*audittypes.StorageProofResult{
			mkResult(bucketOld, "ticket-z"),
			mkResult(bucketRecent, "ticket-m"),
			mkResult(bucketOld, "ticket-a"),
			mkResult(bucketRecent, "ticket-b"),
			mkResult(bucketRecent, "ticket-aa"),
			mkResult(bucketOld, "ticket-c"),
		}
		for _, r := range seqs {
			b.Append(11, r)
		}
		return b.CollectResults(11)
	}
	a := ticketIDsOf(build())
	c := ticketIDsOf(build())
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("non-deterministic output:\n  run1: %v\n  run2: %v", a, c)
	}
}

func TestBuffer_ConcurrentAppendDrain(t *testing.T) {
	b := NewBuffer()
	const writers = 8
	const perWriter = 50

	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				bucket := bucketRecent
				if i%3 == 0 {
					bucket = bucketOld
				}
				b.Append(13, mkResult(bucket, fmt.Sprintf("w%d-i%03d", w, i)))
			}
		}(w)
	}

	// Concurrent drainer racing with writers — also exercises the lock under -race.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = b.CollectResults(13)
			}
		}
	}()

	wg.Wait()
	close(done)
	// Drain leftover (whatever the racing collector didn't drain).
	_ = b.CollectResults(13)

	// Buffer must be empty for the epoch.
	if got := b.CollectResults(13); len(got) != 0 {
		t.Fatalf("expected empty buffer after final drain, got %d", len(got))
	}
}

// TestBuffer_FullModeAssignedTargetCoverageBelowCap is the LEP-6 v3-plan PR3
// item-5 invariant guard: when the dispatcher emits the realistic chain-bound
// workload (one assigned target → one RECENT + one OLD per epoch, far under
// the 16-result cap), the buffer MUST surface both bucket entries for that
// target untouched. This is the only path that runs in production today
// because chain-side AssignTargets returns at most one target per epoch.
//
// Note: the throttle policy ("drop non-RECENT first") does NOT preserve
// per-target RECENT+OLD coverage if the buffer ever exceeds cap. That is
// intentional and acceptable here because the dispatcher is structurally
// bounded to ≤2 emissions per assigned target. If a future change widens
// emissions (e.g. multiple assigned targets per epoch), the throttle policy
// must be revisited — see TestBuffer_OverCap_DropPolicyIsNotTargetAware
// below for the explicit pin of current behavior.
func TestBuffer_FullModeAssignedTargetCoverageBelowCap(t *testing.T) {
	const target = "lumera1assignedtarget000000000000000000target"
	b := NewBuffer()

	// Realistic FULL-mode emission: one RECENT + one OLD for the assigned
	// target, plus a small amount of unrelated-target carryover (e.g. from
	// a parallel challenger run for a different epoch slice).
	b.Append(42, mkResultForTarget(bucketRecent, "ticket-recent-A", target))
	b.Append(42, mkResultForTarget(bucketOld, "ticket-old-A", target))
	b.Append(42, mkResultForTarget(bucketRecent, "ticket-recent-other", "lumera1other00000000000000000000000000other"))
	b.Append(42, mkResultForTarget(bucketOld, "ticket-old-other", "lumera1other00000000000000000000000000other"))

	got := b.CollectResults(42)
	if len(got) != 4 {
		t.Fatalf("want 4 results below cap, got %d", len(got))
	}

	var sawTargetRecent, sawTargetOld bool
	for _, r := range got {
		if r.TargetSupernodeAccount != target {
			continue
		}
		switch r.BucketType {
		case bucketRecent:
			if sawTargetRecent {
				t.Fatalf("duplicate RECENT for assigned target")
			}
			sawTargetRecent = true
		case bucketOld:
			if sawTargetOld {
				t.Fatalf("duplicate OLD for assigned target")
			}
			sawTargetOld = true
		}
	}
	if !sawTargetRecent {
		t.Fatalf("FULL coverage invariant violated: assigned target RECENT entry missing from CollectResults output")
	}
	if !sawTargetOld {
		t.Fatalf("FULL coverage invariant violated: assigned target OLD entry missing from CollectResults output")
	}
}

// TestBuffer_OverCap_FairnessByTargetBucket pins the new H5 throttle policy:
// when over cap, drop oldest from the LARGEST (target, bucket) group first
// so a single noisy target cannot starve other targets.
//
// Setup: 14 RECENT for noisyTarget + 1 RECENT + 2 OLD for assignedTarget +
// 1 OLD for fillerTarget = 18 total. Cap 16, must drop 2.
//   - Largest group is (noisyTarget, RECENT)=14. Phase-1 drops oldest 2
//     entries from this group. All other groups untouched.
//   - assignedTarget's coverage (1 RECENT + 1 OLD) survives.
func TestBuffer_OverCap_FairnessByTargetBucket(t *testing.T) {
	const noisy = "lumera1noisy0000000000000000000000000000noisy"
	const assigned = "lumera1assignedtarget000000000000000000target"
	const filler = "lumera1other00000000000000000000000000other"

	b := NewBuffer()
	for i := 0; i < 14; i++ {
		b.Append(99, mkResultForTarget(bucketRecent, fmt.Sprintf("noisy-recent-%02d", i), noisy))
	}
	b.Append(99, mkResultForTarget(bucketRecent, "assigned-recent-A", assigned))
	b.Append(99, mkResultForTarget(bucketOld, "assigned-old-A", assigned))
	b.Append(99, mkResultForTarget(bucketOld, "assigned-old-B", assigned))
	b.Append(99, mkResultForTarget(bucketOld, "filler-old-zzz", filler))

	got := b.CollectResults(99)
	if len(got) != 16 {
		t.Fatalf("want 16 (cap), got %d", len(got))
	}

	// Both assignedTarget rows (1 RECENT + 2 OLD = 3 entries) must survive.
	var assignedKept int
	for _, r := range got {
		if r.TargetSupernodeAccount == assigned {
			assignedKept++
		}
	}
	if assignedKept != 3 {
		t.Fatalf("fairness violated: assigned target should retain all 3 entries, got %d", assignedKept)
	}
	// Filler (single-entry group) must survive.
	var fillerKept bool
	for _, r := range got {
		if r.TicketId == "filler-old-zzz" {
			fillerKept = true
		}
	}
	if !fillerKept {
		t.Fatalf("fairness violated: single-entry filler group should survive")
	}
	// Noisy target should have lost exactly 2 entries (its two oldest:
	// noisy-recent-00 and noisy-recent-01).
	var noisyKept int
	for _, r := range got {
		if r.TargetSupernodeAccount == noisy {
			noisyKept++
			if r.TicketId == "noisy-recent-00" || r.TicketId == "noisy-recent-01" {
				t.Fatalf("expected oldest noisy entries dropped; %q present", r.TicketId)
			}
		}
	}
	if noisyKept != 12 {
		t.Fatalf("noisy target should keep 12 (14-2), got %d", noisyKept)
	}
}

func TestBuffer_PerEpochIsolation(t *testing.T) {
	b := NewBuffer()
	b.Append(5, mkResult(bucketRecent, "e5-a"))
	b.Append(5, mkResult(bucketOld, "e5-b"))
	b.Append(6, mkResult(bucketRecent, "e6-a"))
	b.Append(6, mkResult(bucketOld, "e6-b"))

	got5 := b.CollectResults(5)
	if len(got5) != 2 {
		t.Fatalf("epoch 5: want 2, got %d", len(got5))
	}
	for _, r := range got5 {
		if r.TicketId != "e5-a" && r.TicketId != "e5-b" {
			t.Fatalf("epoch 5 leaked foreign ticket %q", r.TicketId)
		}
	}

	// Epoch 6 must remain intact.
	got6 := b.CollectResults(6)
	if len(got6) != 2 {
		t.Fatalf("epoch 6 lost data: want 2, got %d", len(got6))
	}
	for _, r := range got6 {
		if r.TicketId != "e6-a" && r.TicketId != "e6-b" {
			t.Fatalf("epoch 6 leaked foreign ticket %q", r.TicketId)
		}
	}
}
