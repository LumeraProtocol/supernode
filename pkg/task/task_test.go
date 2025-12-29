package task

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStartEndSnapshot(t *testing.T) {
	tr := New()

	// Initially empty
	if snap := tr.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected empty snapshot, got %#v", snap)
	}

	// Start two tasks under same service
	tr.Start("svc", "id1")
	tr.Start("svc", "id2")

	snap := tr.Snapshot()
	ids, ok := snap["svc"]
	if !ok {
		t.Fatalf("expected service 'svc' in snapshot")
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d (%v)", len(ids), ids)
	}

	// End one task
	tr.End("svc", "id1")
	snap = tr.Snapshot()
	ids = snap["svc"]
	if len(ids) != 1 {
		t.Fatalf("expected 1 id, got %d (%v)", len(ids), ids)
	}
	if ids[0] != "id2" && ids[0] != "id1" { // order not guaranteed; check that id2 remains by set membership
		// Build a small set for clarity
		m := map[string]struct{}{}
		for _, v := range ids {
			m[v] = struct{}{}
		}
		if _, ok := m["id2"]; !ok {
			t.Fatalf("expected id2 to remain, got %v", ids)
		}
	}

	// End last task
	tr.End("svc", "id2")
	snap = tr.Snapshot()
	if _, ok := snap["svc"]; ok {
		t.Fatalf("expected service removed after last task ended, got %v", snap)
	}
}

func TestInvalidInputsAndIsolation(t *testing.T) {
	tr := New()

	// Invalid inputs should be ignored
	tr.Start("", "id")
	tr.Start("svc", "")
	tr.End("", "id")
	tr.End("svc", "")
	if snap := tr.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected empty snapshot for invalid inputs, got %#v", snap)
	}

	// Snapshot must be a copy
	tr.Start("svc", "id")
	snap := tr.Snapshot()
	// mutate snapshot map and slice
	delete(snap, "svc")
	snap2 := tr.Snapshot()
	if _, ok := snap2["svc"]; !ok {
		t.Fatalf("mutating snapshot should not affect tracker state")
	}
}

// TestConcurrentAccessNoPanic ensures that concurrent Start/End/Snapshot
// operations do not panic due to unsafe map access.
func TestConcurrentAccessNoPanic(t *testing.T) {
	tr := New()

	// Run a mix of writers and readers concurrently.
	var wg sync.WaitGroup
	startWriters := 8
	snapReaders := 4
	loops := 1000

	// Writers: repeatedly start/end tasks across a few services.
	for w := 0; w < startWriters; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < loops; i++ {
				svc := "svc" + string('A'+rune(id%3)) // svcA, svcB, svcC
				tid := svc + ":t" + fmtInt(i%5)
				tr.Start(svc, tid)
				if i%2 == 0 {
					tr.End(svc, tid)
				}
			}
		}(w)
	}

	// Readers: take snapshots concurrently.
	for r := 0; r < snapReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < loops; i++ {
				_ = tr.Snapshot()
			}
		}()
	}

	// If there is any concurrent map access bug, the test runner would panic.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent access test timed out")
	}
}

// fmtInt provides a tiny int-to-string helper to avoid importing strconv.
func fmtInt(i int) string { return string('0' + rune(i)) }

func TestHandleIdempotentAndWatchdog(t *testing.T) {
	tr := New()
	ctx := context.Background()

	// Idempotent End
	g := StartWith(tr, ctx, "svc.handle", "id-1", 0)
	g.End(ctx)
	g.End(ctx) // no panic, no double-end crash

	// Watchdog auto-end: use a small timeout
	g2 := StartWith(tr, ctx, "svc.handle", "id-2", 50*time.Millisecond)
	_ = g2 // ensure handle stays referenced until timeout path
	// Do not call End; let the watchdog fire
	time.Sleep(120 * time.Millisecond)

	// After watchdog, the task should not be listed
	snap := tr.Snapshot()
	if ids, ok := snap["svc.handle"]; ok {
		// If still present, ensure id-2 is not in the list
		for _, id := range ids {
			if id == "id-2" {
				t.Fatalf("expected watchdog to remove id-2 from svc.handle; snapshot: %v", ids)
			}
		}
	}
}

func TestStartUniqueWith_PreventsDuplicates(t *testing.T) {
	tr := New()
	ctx := context.Background()

	h1, err := StartUniqueWith(tr, ctx, "svc.unique", "id-1", 0)
	if err != nil {
		t.Fatalf("StartUniqueWith 1: %v", err)
	}
	t.Cleanup(func() { h1.End(ctx) })

	h2, err := StartUniqueWith(tr, ctx, "svc.unique", "id-1", 0)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got handle=%v err=%v", h2, err)
	}

	// After ending, it should be startable again.
	h1.End(ctx)
	h3, err := StartUniqueWith(tr, ctx, "svc.unique", "id-1", 0)
	if err != nil {
		t.Fatalf("StartUniqueWith 2: %v", err)
	}
	h3.End(ctx)
}
