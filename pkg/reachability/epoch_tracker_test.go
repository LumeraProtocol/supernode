package reachability

import "testing"

func TestEpochTrackerPerServiceBooleanAndPrune(t *testing.T) {
	tr := NewEpochTracker(3)

	tr.Mark(10, ServiceGRPC)
	tr.Mark(10, ServiceGRPC)
	tr.Mark(10, ServiceGateway)

	if !tr.Seen(10, ServiceGRPC) {
		t.Fatalf("expected grpc seen")
	}
	if !tr.Seen(10, ServiceGateway) {
		t.Fatalf("expected gateway seen")
	}
	if tr.Seen(10, ServiceP2P) {
		t.Fatalf("expected p2p not seen")
	}

	tr.Mark(8, ServiceP2P)
	if !tr.Seen(8, ServiceP2P) {
		t.Fatalf("expected epoch 8 seen before prune")
	}

	tr.Mark(12, ServiceGRPC) // prune epochs < 10
	if tr.Seen(8, ServiceP2P) {
		t.Fatalf("expected epoch 8 pruned")
	}
	if !tr.Seen(10, ServiceGRPC) {
		t.Fatalf("expected epoch 10 retained")
	}
}
