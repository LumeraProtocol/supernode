package p2p

import "testing"

type peersCounter interface {
	PeersCount() int
}

var _ peersCounter = (*p2p)(nil)

func TestPeersCount_NilReceiver_ReturnsZero(t *testing.T) {
	var s *p2p
	if got := s.PeersCount(); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestPeersCount_NilDHT_ReturnsZero(t *testing.T) {
	s := &p2p{}
	if got := s.PeersCount(); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

