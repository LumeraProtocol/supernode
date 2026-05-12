package kademlia

import (
	"context"
	"testing"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
)

// I1 + I2 + I6: state classification SSoT.
func TestStateClassification_Table(t *testing.T) {
	cases := []struct {
		state           sntypes.SuperNodeState
		routingEligible bool
		storeEligible   bool
	}{
		{sntypes.SuperNodeStateUnspecified, false, false},
		{sntypes.SuperNodeStateActive, true, true},
		{sntypes.SuperNodeStateDisabled, false, false},
		{sntypes.SuperNodeStateStopped, false, false},
		{sntypes.SuperNodeStatePenalized, false, false},
		{sntypes.SuperNodeStatePostponed, true, false},
		{sntypes.SuperNodeStateStorageFull, true, false},
	}
	for _, tc := range cases {
		s := snStateInt(tc.state)
		if got := isRoutingEligibleState(s); got != tc.routingEligible {
			t.Errorf("isRoutingEligibleState(%s) = %v, want %v", tc.state, got, tc.routingEligible)
		}
		if got := isStoreEligibleState(s); got != tc.storeEligible {
			t.Errorf("isStoreEligibleState(%s) = %v, want %v", tc.state, got, tc.storeEligible)
		}
	}
}

// I9: self-store-eligibility gate. Verify allow-during-bootstrap, integration
// test bypass, and post-init gate behavior.
func TestSelfStoreEligible(t *testing.T) {
	t.Run("pre-init_allows_to_avoid_lockout", func(t *testing.T) {
		d := &DHT{}
		if !d.selfStoreEligible() {
			t.Fatalf("pre-init should be permissive")
		}
	})

	t.Run("post-init_active_allows", func(t *testing.T) {
		d := &DHT{}
		d.setSelfState(snStateInt(sntypes.SuperNodeStateActive))
		if !d.selfStoreEligible() {
			t.Fatalf("ACTIVE must be store-eligible")
		}
	})

	t.Run("post-init_storage_full_rejects", func(t *testing.T) {
		d := &DHT{}
		d.setSelfState(snStateInt(sntypes.SuperNodeStateStorageFull))
		if d.selfStoreEligible() {
			t.Fatalf("STORAGE_FULL must NOT be store-eligible")
		}
	})

	t.Run("post-init_postponed_rejects", func(t *testing.T) {
		d := &DHT{}
		d.setSelfState(snStateInt(sntypes.SuperNodeStatePostponed))
		if d.selfStoreEligible() {
			t.Fatalf("POSTPONED must NOT be store-eligible")
		}
	})
}

// I3/I9: shouldRejectStore contract.
func TestShouldRejectStore(t *testing.T) {
	cases := []struct {
		name       string
		selfState  *sntypes.SuperNodeState // nil = pre-init
		newKeys    int
		wantReject bool
	}{
		{"pre-init_any_allows", nil, 10, false},
		{"active_with_new_keys_allows", ptrState(sntypes.SuperNodeStateActive), 5, false},
		{"active_zero_new_keys_allows", ptrState(sntypes.SuperNodeStateActive), 0, false},
		{"storage_full_zero_new_keys_allows_replication", ptrState(sntypes.SuperNodeStateStorageFull), 0, false},
		{"storage_full_one_new_key_rejects", ptrState(sntypes.SuperNodeStateStorageFull), 1, true},
		{"postponed_new_keys_rejects", ptrState(sntypes.SuperNodeStatePostponed), 7, true},
		{"disabled_new_keys_rejects", ptrState(sntypes.SuperNodeStateDisabled), 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &DHT{}
			if tc.selfState != nil {
				d.setSelfState(snStateInt(*tc.selfState))
			}
			if got := d.shouldRejectStore(tc.newKeys); got != tc.wantReject {
				t.Fatalf("shouldRejectStore = %v, want %v", got, tc.wantReject)
			}
		})
	}
}

func ptrState(s sntypes.SuperNodeState) *sntypes.SuperNodeState { return &s }

// I9 server-ingress semantic: STORAGE_FULL/POSTPONED STILL serves reads —
// verified by the fact that read-path gates only consult eligibleForRouting
// (see TestEligibleForRouting_PreInit_AndPopulated + the stricter-containment
// test), and shouldRejectStore only fires on newKeys>0.
func TestEligibleForRouting_PreInit_AndPopulated(t *testing.T) {
	ctx := context.Background()

	// Pre-init: routing gate disabled, everything eligible to avoid lockout.
	d := &DHT{}
	n := &Node{ID: []byte("peerA")}
	if !d.eligibleForRouting(n) {
		t.Fatalf("pre-init eligibleForRouting should return true")
	}

	// Populate an empty allowlist post-init: no-op (ready stays false).
	d.setRoutingAllowlist(ctx, map[[32]byte]struct{}{})
	if !d.eligibleForRouting(n) {
		t.Fatalf("empty allowlist pre-ready should still allow")
	}

	// Populate with one peer's hash.
	n.SetHashedID()
	var key [32]byte
	copy(key[:], n.HashedID)
	d.setRoutingAllowlist(ctx, map[[32]byte]struct{}{key: {}})
	if !d.eligibleForRouting(n) {
		t.Fatalf("peer in allowlist must be eligible")
	}

	// Peer not in allowlist is rejected.
	other := &Node{ID: []byte("peerB")}
	if d.eligibleForRouting(other) {
		t.Fatalf("peer not in allowlist must be rejected")
	}
}

// I2 admission: store allowlist is stricter than routing.
func TestEligibleForStore_StrictlyContainedInRouting(t *testing.T) {
	ctx := context.Background()
	d := &DHT{}

	active := &Node{ID: []byte("active")}
	active.SetHashedID()
	storageFull := &Node{ID: []byte("storage_full")}
	storageFull.SetHashedID()

	var ak, sk [32]byte
	copy(ak[:], active.HashedID)
	copy(sk[:], storageFull.HashedID)

	d.setRoutingAllowlist(ctx, map[[32]byte]struct{}{ak: {}, sk: {}})
	d.setStoreAllowlist(ctx, map[[32]byte]struct{}{ak: {}}) // STORAGE_FULL omitted

	if !d.eligibleForRouting(active) || !d.eligibleForRouting(storageFull) {
		t.Fatalf("both peers must be routing-eligible")
	}
	if !d.eligibleForStore(active) {
		t.Fatalf("ACTIVE peer must be store-eligible")
	}
	if d.eligibleForStore(storageFull) {
		t.Fatalf("STORAGE_FULL peer must NOT be store-eligible")
	}
}
