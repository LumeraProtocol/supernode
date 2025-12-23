package supernode_metrics

import (
	"reflect"
	"testing"
)

func TestBuildAssignmentDeterministicAndExpectedInboundMatches(t *testing.T) {
	senders := []probeTarget{{identity: "a"}, {identity: "b"}, {identity: "c"}}
	receivers := []probeTarget{{identity: "a"}, {identity: "b"}, {identity: "c"}, {identity: "postponed"}}
	rand := []byte{1, 2, 3, 4}

	a1 := buildAssignment("chain-1", 42, rand, senders, receivers, 3)
	a2 := buildAssignment("chain-1", 42, rand, senders, receivers, 3)

	if !reflect.DeepEqual(a1.bySender, a2.bySender) {
		t.Fatalf("bySender not deterministic: %v vs %v", a1.bySender, a2.bySender)
	}
	if !reflect.DeepEqual(a1.expected, a2.expected) {
		t.Fatalf("expected not deterministic: %v vs %v", a1.expected, a2.expected)
	}

	recount := map[string]int{}
	for _, targets := range a1.bySender {
		for _, target := range targets {
			recount[target]++
		}
	}
	if !reflect.DeepEqual(recount, a1.expected) {
		t.Fatalf("expected inbound mismatch: %v vs %v", recount, a1.expected)
	}

	for sender, targets := range a1.bySender {
		if len(targets) != 3 {
			t.Fatalf("sender %q: expected 3 targets got %d (%v)", sender, len(targets), targets)
		}
		for _, target := range targets {
			if target == sender {
				t.Fatalf("sender %q assigned to itself: %v", sender, targets)
			}
		}
	}
}
