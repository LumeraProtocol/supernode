package kademlia

import (
	"context"
	"strings"
	"testing"
)

func TestIterateBatchStore_NoCandidateNodes_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ht, err := NewHashTable(&Options{
		ID:   []byte("self"),
		IP:   "0.0.0.0",
		Port: 4445,
	})
	if err != nil {
		t.Fatalf("NewHashTable: %v", err)
	}

	dht := &DHT{
		ht:         ht,
		ignorelist: NewBanList(ctx),
	}

	err = dht.IterateBatchStore(ctx, [][]byte{[]byte("value")}, 0, "task")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no eligible store peers") {
		t.Fatalf("unexpected error: %v", err)
	}
}
