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

func TestBuffer_AboveCap_DropsNonRecentFirst(t *testing.T) {
	b := NewBuffer()
	// 10 RECENT + 8 OLD = 18 total, cap 16 → drop 2 OLD oldest. Kept: 10 R + 6 O.
	for i := 0; i < 10; i++ {
		b.Append(7, mkResult(bucketRecent, fmt.Sprintf("recent-%02d", i)))
	}
	for i := 0; i < 8; i++ {
		b.Append(7, mkResult(bucketOld, fmt.Sprintf("old-%02d", i)))
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
	if nRecent != 10 || nOld != 6 {
		t.Fatalf("want 10 RECENT + 6 OLD, got %d RECENT + %d OLD", nRecent, nOld)
	}
	// The two oldest OLD entries by ticket_id ("old-00", "old-01") must be the dropped ones.
	for _, r := range got {
		if r.TicketId == "old-00" || r.TicketId == "old-01" {
			t.Fatalf("expected oldest OLD entries dropped; %q present", r.TicketId)
		}
	}
}

func TestBuffer_AboveCap_OnlyRecent_DropsOldest(t *testing.T) {
	b := NewBuffer()
	// 20 RECENT, cap 16 → drop 4 oldest by ticket_id lex.
	for i := 0; i < 20; i++ {
		b.Append(9, mkResult(bucketRecent, fmt.Sprintf("r-%02d", i)))
	}
	got := b.CollectResults(9)
	if len(got) != 16 {
		t.Fatalf("want 16 results, got %d", len(got))
	}
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

// TestBuffer_OverCap_DropPolicyIsNotTargetAware pins the documented limitation
// of the current throttle: "drop non-RECENT first" is target-blind, so an
// assigned target's OLD entry CAN be dropped if the buffer ever exceeds 16.
// This is acceptable today because the dispatcher cannot realistically push
// the buffer over cap (chain assigns ≤1 target/epoch → ≤2 emissions). If this
// invariant ever changes, this test will catch the silent regression and force
// a target-aware throttle revision (see LEP-6 v3 plan §3 PR3 item 6, deferred
// to PR-4 ownership for heal-op driven multi-target scenarios).
func TestBuffer_OverCap_DropPolicyIsNotTargetAware(t *testing.T) {
	const assignedTarget = "lumera1assignedtarget000000000000000000target"
	const otherTarget = "lumera1other00000000000000000000000000other"

	b := NewBuffer()
	// 14 RECENT for unrelated target + 1 RECENT + 1 OLD + 1 OLD (filler) for
	// assigned target = 17 total → throttle drops 1 non-RECENT (oldest by
	// ticket_id lex). The assigned target's OLD entry is at risk if its
	// ticket_id sorts earlier than the filler's.
	for i := 0; i < 14; i++ {
		b.Append(99, mkResultForTarget(bucketRecent, fmt.Sprintf("other-recent-%02d", i), otherTarget))
	}
	b.Append(99, mkResultForTarget(bucketRecent, "assigned-recent-A", assignedTarget))
	b.Append(99, mkResultForTarget(bucketOld, "assigned-old-A", assignedTarget))
	b.Append(99, mkResultForTarget(bucketOld, "filler-old-zzz", otherTarget))

	got := b.CollectResults(99)
	if len(got) != 16 {
		t.Fatalf("want 16 (cap), got %d", len(got))
	}

	// Document current behavior: dropped one OLD by lex order. Either
	// "assigned-old-A" or "filler-old-zzz" survives — current "drop oldest
	// non-RECENT by ticket_id lex" implementation drops "assigned-old-A"
	// because it sorts before "filler-old-zzz". This is the behavior pin —
	// if a future change makes throttle target-aware (preserve assigned-target
	// coverage even over cap), update this test accordingly.
	var assignedOldKept, fillerOldKept bool
	for _, r := range got {
		switch r.TicketId {
		case "assigned-old-A":
			assignedOldKept = true
		case "filler-old-zzz":
			fillerOldKept = true
		}
	}
	if assignedOldKept {
		t.Fatalf("throttle became target-aware (kept assigned-target OLD) — update test or note the policy change")
	}
	if !fillerOldKept {
		t.Fatalf("expected filler-old-zzz to survive (lex-greater non-RECENT survives drop-oldest policy); got dropped")
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
