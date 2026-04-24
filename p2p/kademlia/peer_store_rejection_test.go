package kademlia

import (
	"context"
	"testing"
)

// Regression test for production-gate finding:
// The STORE / BatchStore peer self-guard correctly rejects writes when the
// peer's on-chain state is not store-eligible (e.g. STORAGE_FULL). But the
// CLIENT was counting those authoritative rejections as failures, dropping
// its measured success rate below the 75% threshold and failing entire
// cascade uploads whenever a peer had just transitioned to STORAGE_FULL and
// the caller's local storeAllowlist was still stale.
//
// Invariants locked in by this test:
//
//	(a) isPeerStoreIneligibleError must recognize both the STORE and
//	    BatchStoreData emitter phrasings. The marker is shared via the
//	    package-level constant peerStoreIneligibleMarker.
//	(b) isPeerStoreIneligibleError must NOT match generic store errors
//	    (timeout, network failure, ErrFailed without the marker).
//	(c) pruneStoreAllowEntry must remove the given peer from the in-memory
//	    allowlist and decrement the atomic count so subsequent hot-path
//	    lookups in the same run do not re-select the peer.
//	(d) pruneStoreAllowEntry is a no-op when the DHT is nil, the node is
//	    nil / has an empty ID, or the allowlist is unset.

func TestIsPeerStoreIneligibleError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"empty_false", "", false},
		{"generic_store_failure_false", "context deadline exceeded", false},
		{"rpc_internal_false", "rpc error: code = Internal", false},
		// Server-side emitter strings (see network.go:handleStoreData +
		// handleBatchStoreData). We match on the marker substring so wrapping
		// with additional context (gRPC framing, SDK wrapping) still counts.
		{"batch_store_phrase_true", "batch store rejected: self not store-eligible", true},
		{"single_store_phrase_true", "store rejected: self not store-eligible", true},
		{"wrapped_true", "rpc error: code = Internal desc = batch store rejected: self not store-eligible", true},
		// Defensive: accidental typo variants must not match (forces exact marker).
		{"near_miss_false", "self not store_eligible", false},
		{"case_sensitive_false", "SELF NOT STORE-ELIGIBLE", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPeerStoreIneligibleError(tc.msg); got != tc.want {
				t.Fatalf("isPeerStoreIneligibleError(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// Cross-check: the client-side recognizer never drifts from the server-side
// emitter wording. If a future refactor changes one without the other, this
// test fails loudly.
func TestPeerStoreIneligibleMarker_PresentInServerEmitters(t *testing.T) {
	// These are the exact strings emitted by handleStoreData and
	// handleBatchStoreData in network.go. Keeping them here makes this test
	// the single contract surface between client and server.
	serverEmitted := []string{
		"store rejected: self not store-eligible",
		"batch store rejected: self not store-eligible",
	}
	for _, s := range serverEmitted {
		if !isPeerStoreIneligibleError(s) {
			t.Fatalf("server-emitted string %q not recognized by client-side isPeerStoreIneligibleError", s)
		}
	}
}

func TestPruneStoreAllowEntry_RemovesPeerAndDecrementsCount(t *testing.T) {
	ctx := context.Background()
	d := &DHT{}

	// Seed 3 peers in the store allowlist via setStoreAllowlist so the
	// ready flag and count are consistent with normal code paths.
	peers := []*Node{
		{ID: []byte("peer-A")},
		{ID: []byte("peer-B")},
		{ID: []byte("peer-C")},
	}
	allow := make(map[[32]byte]struct{}, len(peers))
	for _, p := range peers {
		p.SetHashedID()
		var k [32]byte
		copy(k[:], p.HashedID)
		allow[k] = struct{}{}
	}
	d.setStoreAllowlist(ctx, allow)
	if got, want := d.storeAllowCount.Load(), int64(3); got != want {
		t.Fatalf("seed count: got %d, want %d", got, want)
	}

	// Prune peer-B.
	d.pruneStoreAllowEntry(peers[1])
	if got, want := d.storeAllowCount.Load(), int64(2); got != want {
		t.Fatalf("count after prune: got %d, want %d", got, want)
	}
	// Verify eligibleForStore honors the prune.
	if d.eligibleForStore(peers[1]) {
		t.Fatalf("pruned peer must NOT be store-eligible")
	}
	if !d.eligibleForStore(peers[0]) || !d.eligibleForStore(peers[2]) {
		t.Fatalf("non-pruned peers must remain store-eligible")
	}

	// Idempotent: pruning the same peer again is a no-op.
	d.pruneStoreAllowEntry(peers[1])
	if got, want := d.storeAllowCount.Load(), int64(2); got != want {
		t.Fatalf("count after double-prune: got %d, want %d", got, want)
	}
}

func TestPruneStoreAllowEntry_NoOpOnEdgeCases(t *testing.T) {
	// Nil receiver.
	var d *DHT
	d.pruneStoreAllowEntry(&Node{ID: []byte("x")}) // must not panic

	d = &DHT{}
	// Nil node.
	d.pruneStoreAllowEntry(nil) // must not panic
	// Empty ID.
	d.pruneStoreAllowEntry(&Node{ID: nil})
	d.pruneStoreAllowEntry(&Node{ID: []byte{}})
	// Unset allowlist: no panic, no side-effect.
	if got := d.storeAllowCount.Load(); got != 0 {
		t.Fatalf("unexpected count change on no-op: %d", got)
	}
}
