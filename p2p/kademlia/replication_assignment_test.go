package kademlia

import (
	"context"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/domain"
)

func TestAssignReplicationKeysToPeers(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)
	t2 := t1.Add(time.Second)

	replicationKeys := domain.KeysWithTimestamp{
		{Key: "00", CreatedAt: t1},
		{Key: "01", CreatedAt: t2},
		{Key: "zz", CreatedAt: t2}, // invalid hex; should be skipped
	}

	peerStart := map[string]time.Time{
		"peerA": t1, // should receive only key "01" (strictly after)
		"peerB": t0, // should receive keys "00" and "01"
	}

	out := assignReplicationKeysToPeers(context.Background(), replicationKeys, peerStart, func(_ []byte) [][]byte {
		return [][]byte{[]byte("peerA"), []byte("peerB"), []byte("peerC")} // peerC not present in peerStart
	})

	if got := out["peerA"]; len(got) != 1 || got[0] != "01" {
		t.Fatalf("peerA keys: %#v", got)
	}
	if got := out["peerB"]; len(got) != 2 || got[0] != "00" || got[1] != "01" {
		t.Fatalf("peerB keys: %#v", got)
	}
	if got := out["peerC"]; len(got) != 0 {
		t.Fatalf("peerC should not be assigned keys, got %#v", got)
	}
}
