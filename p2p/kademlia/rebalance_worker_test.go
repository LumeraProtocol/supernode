package kademlia

import "testing"

func TestRebalanceOwnersCappedToAlpha(t *testing.T) {
	candidates := make([]*Node, 0, Alpha+2)
	for i := 0; i < Alpha+2; i++ {
		candidates = append(candidates, &Node{ID: []byte{byte(i + 1)}})
	}

	owners := rebalanceOwners(candidates)
	if len(owners) != Alpha {
		t.Fatalf("expected %d owners, got %d", Alpha, len(owners))
	}

	shortOwners := rebalanceOwners(candidates[:2])
	if len(shortOwners) != 2 {
		t.Fatalf("expected 2 owners for short candidate list, got %d", len(shortOwners))
	}
}

func TestRebalanceOwnerDecision(t *testing.T) {
	selfID := []byte("self")

	withSelfAsOwner := make([]*Node, 0, Alpha+1)
	withSelfAsOwner = append(withSelfAsOwner, &Node{ID: selfID})
	for i := 0; i < Alpha; i++ {
		withSelfAsOwner = append(withSelfAsOwner, &Node{ID: []byte{byte(i + 1)}})
	}
	if !containsNodeID(rebalanceOwners(withSelfAsOwner), selfID) {
		t.Fatalf("expected self to be owner when included in first %d candidates", Alpha)
	}

	withSelfOutsideOwnerSet := make([]*Node, 0, Alpha+1)
	for i := 0; i < Alpha; i++ {
		withSelfOutsideOwnerSet = append(withSelfOutsideOwnerSet, &Node{ID: []byte{byte(i + 1)}})
	}
	withSelfOutsideOwnerSet = append(withSelfOutsideOwnerSet, &Node{ID: selfID})
	if containsNodeID(rebalanceOwners(withSelfOutsideOwnerSet), selfID) {
		t.Fatalf("expected self to be non-owner when outside first %d candidates", Alpha)
	}
}

func TestRebalanceShouldHeal(t *testing.T) {
	if !rebalanceShouldHeal(true, Alpha-1, rebalanceMaxHealsPerCycle-1) {
		t.Fatalf("expected heal to be allowed under cap for owner with under-replicated key")
	}
	if rebalanceShouldHeal(false, Alpha-1, 0) {
		t.Fatalf("expected heal to be blocked for non-owner")
	}
	if rebalanceShouldHeal(true, Alpha, 0) {
		t.Fatalf("expected heal to be blocked when holder count already meets minimum")
	}
	if rebalanceShouldHeal(true, Alpha-1, rebalanceMaxHealsPerCycle) {
		t.Fatalf("expected heal to be blocked when heal cap is reached")
	}
}

func TestRebalanceDeleteDecisions(t *testing.T) {
	if !rebalanceShouldTrackDeleteConfirm(false, Alpha) {
		t.Fatalf("expected delete confirmation tracking for non-owner with sufficient holders")
	}
	if rebalanceShouldTrackDeleteConfirm(true, Alpha) {
		t.Fatalf("expected delete confirmation tracking to be blocked for owner")
	}
	if rebalanceShouldTrackDeleteConfirm(false, Alpha-1) {
		t.Fatalf("expected delete confirmation tracking to be blocked when under replicated")
	}

	if !rebalanceShouldDelete(rebalanceDeleteConfirmCycles, rebalanceMaxDeletesPerCycle-1) {
		t.Fatalf("expected delete to be allowed after confirm threshold and under delete cap")
	}
	if rebalanceShouldDelete(rebalanceDeleteConfirmCycles-1, 0) {
		t.Fatalf("expected delete to be blocked before confirm threshold")
	}
	if rebalanceShouldDelete(rebalanceDeleteConfirmCycles, rebalanceMaxDeletesPerCycle) {
		t.Fatalf("expected delete to be blocked when delete cap is reached")
	}
}
