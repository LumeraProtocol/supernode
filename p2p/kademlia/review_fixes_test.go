package kademlia

import (
	"context"
	"testing"
	"time"
)

// Regression tests for three issues raised on PR #284 post-merge review:
//
//   (1) setStoreAllowlist must retain the previous allowlist when given an
//       empty map post-ready (symmetric with setRoutingAllowlist).
//   (2) eligibleForStore must early-return false on nil node before
//       consulting storeAllowReady (defense-in-depth consistency with
//       eligibleForRouting).
//   (3) MaybeRefreshAllowlists must debounce opportunistic refreshes from
//       the IterateBatchStore hot path so concurrent uploads cannot fan
//       out to one chain query per batch.
//
// shouldRejectBatchStore is covered indirectly via shouldRejectStore + the
// existing STORE-path tests; here we add a direct test for the short-circuit
// and the new-key counting path.

// --- (1) setStoreAllowlist empty-map guard ---

func TestSetStoreAllowlist_EmptyMapPostReady_RetainsPrevious(t *testing.T) {
	ctx := context.Background()
	d := &DHT{}

	// Seed a non-empty allowlist so ready=true, count=2.
	seed := func(ids ...string) map[[32]byte]struct{} {
		m := make(map[[32]byte]struct{}, len(ids))
		for _, id := range ids {
			n := &Node{ID: []byte(id)}
			n.SetHashedID()
			var k [32]byte
			copy(k[:], n.HashedID)
			m[k] = struct{}{}
		}
		return m
	}
	d.setStoreAllowlist(ctx, seed("peer-A", "peer-B"))
	if got, want := d.storeAllowCount.Load(), int64(2); got != want {
		t.Fatalf("seed count: got %d, want %d", got, want)
	}
	if !d.storeAllowReady.Load() {
		t.Fatalf("seed should mark ready")
	}

	// Simulate a transient chain hiccup: empty map. Post-ready this MUST
	// be rejected (retaining the previous allowlist) to avoid blocking
	// all writes network-wide.
	d.setStoreAllowlist(ctx, map[[32]byte]struct{}{})

	if got, want := d.storeAllowCount.Load(), int64(2); got != want {
		t.Fatalf("after empty update post-ready: count got %d, want %d (previous allowlist must be retained)", got, want)
	}
	// And the seeded peers must still be eligible.
	a := &Node{ID: []byte("peer-A")}
	if !d.eligibleForStore(a) {
		t.Fatalf("peer-A must remain store-eligible after empty-map update was rejected")
	}
}

func TestSetStoreAllowlist_EmptyMapPreReady_NoOp(t *testing.T) {
	ctx := context.Background()
	d := &DHT{}
	// Pre-ready empty update must not flip ready=true with count=0; that
	// would immediately fail eligibleForStore for every peer during bootstrap.
	d.setStoreAllowlist(ctx, map[[32]byte]struct{}{})
	if d.storeAllowReady.Load() {
		t.Fatalf("pre-ready empty update must not flip ready=true")
	}
	if d.storeAllowCount.Load() != 0 {
		t.Fatalf("pre-ready empty update must not set a non-zero count")
	}
}

// --- (2) eligibleForStore nil-check ordering ---

func TestEligibleForStore_NilNode_ReturnsFalseEvenPreReady(t *testing.T) {
	// Pre-ready the old code returned true for ANY input (including nil),
	// matching the "don't lock out during bootstrap" intent but creating a
	// nil-pointer risk downstream. The fix moves the nil check to the top
	// so a nil *Node is always rejected regardless of ready state. Mirrors
	// eligibleForRouting's ordering.
	d := &DHT{}
	if d.eligibleForStore(nil) {
		t.Fatalf("eligibleForStore(nil) must be false pre-ready")
	}
	if d.eligibleForStore(&Node{ID: nil}) {
		t.Fatalf("eligibleForStore(empty-ID) must be false pre-ready")
	}
	if d.eligibleForStore(&Node{ID: []byte{}}) {
		t.Fatalf("eligibleForStore(empty-slice-ID) must be false pre-ready")
	}

	// Non-nil real node pre-ready must still be eligible (bootstrap permissiveness).
	n := &Node{ID: []byte("peer-real")}
	if !d.eligibleForStore(n) {
		t.Fatalf("eligibleForStore(valid node) must be true pre-ready (bootstrap permissive)")
	}
}

// --- (3) MaybeRefreshAllowlists debounce ---

func TestMaybeRefreshAllowlists_Debounces(t *testing.T) {
	ctx := context.Background()
	d := &DHT{}
	// integrationTestEnabled() returns true under INTEGRATION_TEST; assume
	// it is not set. If your local env sets it, skip.
	if integrationTestEnabled() {
		t.Skip("INTEGRATION_TEST set; MaybeRefreshAllowlists is a no-op in that mode")
	}

	// Simulate a very recent successful refresh.
	d.lastAllowlistRefreshUnixNano.Store(time.Now().UnixNano())

	// Within the debounce window this MUST skip (no chain RPC).
	// Because d.options.LumeraClient is nil, RefreshAllowlistsFromChain
	// would return nil (guard at the top); but the debounce is supposed
	// to short-circuit BEFORE reaching any of that logic. We assert by
	// checking the return value (refreshed=false => skipped).
	refreshed, err := d.MaybeRefreshAllowlists(ctx)
	if err != nil {
		t.Fatalf("unexpected err from skipped call: %v", err)
	}
	if refreshed {
		t.Fatalf("MaybeRefreshAllowlists must skip within the debounce window")
	}
}

func TestMaybeRefreshAllowlists_FiresWhenStale(t *testing.T) {
	ctx := context.Background()
	d := &DHT{}
	if integrationTestEnabled() {
		t.Skip("INTEGRATION_TEST set; MaybeRefreshAllowlists is a no-op in that mode")
	}

	// No prior refresh (last == 0). MaybeRefreshAllowlists must attempt a
	// refresh. Because LumeraClient is nil, RefreshAllowlistsFromChain
	// returns nil silently and stamps the timestamp — so refreshed=true.
	refreshed, err := d.MaybeRefreshAllowlists(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !refreshed {
		t.Fatalf("MaybeRefreshAllowlists must fire when no prior refresh has been recorded")
	}
	if d.lastAllowlistRefreshUnixNano.Load() == 0 {
		t.Fatalf("lastAllowlistRefreshUnixNano must be stamped after a successful refresh attempt")
	}

	// Now stamp the prior refresh outside the debounce window (e.g. 60s ago).
	d.lastAllowlistRefreshUnixNano.Store(time.Now().Add(-60 * time.Second).UnixNano())
	refreshed, err = d.MaybeRefreshAllowlists(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !refreshed {
		t.Fatalf("MaybeRefreshAllowlists must fire when the last refresh is older than allowlistOpportunisticMinInterval")
	}
}

// --- (helper) shouldRejectBatchStore contract ---
//
// We can't easily unit-test the full shouldRejectBatchStore because it touches
// a storage backend. Instead we verify the short-circuit: when self is
// store-eligible, the function returns (false, 0) WITHOUT touching storage.
// We assert "without touching storage" by passing a nil store and confirming
// no panic.

func TestShouldRejectBatchStore_ShortCircuitsOnSelfEligible(t *testing.T) {
	d := &DHT{}
	// No selfState set => pre-init => selfStoreEligible()==true => short-circuit.
	// Pass arbitrary payload; if the function reached storage, we'd panic
	// on nil d.store. The short-circuit must bypass the loop entirely.
	reject, newKeys := d.shouldRejectBatchStore(context.Background(), [][]byte{[]byte("x"), []byte("y")})
	if reject {
		t.Fatalf("must not reject when self is store-eligible")
	}
	if newKeys != 0 {
		t.Fatalf("short-circuit must return newKeys=0 without counting; got %d", newKeys)
	}
}

// Sanity: defensive nil receiver.
func TestShouldRejectBatchStore_NilReceiver(t *testing.T) {
	var d *DHT
	reject, n := d.shouldRejectBatchStore(context.Background(), [][]byte{[]byte("x")})
	if reject || n != 0 {
		t.Fatalf("nil receiver must return (false, 0); got (%v, %d)", reject, n)
	}
}
