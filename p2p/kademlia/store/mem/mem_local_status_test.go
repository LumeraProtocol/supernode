package mem

import (
	"context"
	"encoding/hex"
	"testing"
)

func TestRetrieveBatchLocalStatus_ByHexKey(t *testing.T) {
	s := NewStore()
	key := []byte{0x01, 0x02, 0xab, 0xcd}
	value := []byte("value")

	if err := s.Store(context.Background(), key, value, 0, false); err != nil {
		t.Fatalf("store key: %v", err)
	}

	keyHex := hex.EncodeToString(key)
	statuses, err := s.RetrieveBatchLocalStatus(context.Background(), []string{keyHex})
	if err != nil {
		t.Fatalf("retrieve batch local status: %v", err)
	}

	status := statuses[keyHex]
	if !status.Exists {
		t.Fatalf("expected key to exist")
	}
	if !status.HasLocalBlob {
		t.Fatalf("expected key to have local blob")
	}
	if status.DataLen != len(value) {
		t.Fatalf("expected data len %d, got %d", len(value), status.DataLen)
	}
}

func TestListLocalKeysPage_ReturnsHexEncodedSortedKeys(t *testing.T) {
	s := NewStore()
	k1 := []byte{0x00, 0x00, 0x00, 0x02}
	k2 := []byte{0x00, 0x00, 0x00, 0x01}

	if err := s.Store(context.Background(), k1, []byte("v1"), 0, false); err != nil {
		t.Fatalf("store key1: %v", err)
	}
	if err := s.Store(context.Background(), k2, []byte("v2"), 0, false); err != nil {
		t.Fatalf("store key2: %v", err)
	}

	keys, err := s.ListLocalKeysPage(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("list local keys page: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}

	expectedFirst := hex.EncodeToString(k2)
	expectedSecond := hex.EncodeToString(k1)
	if keys[0] != expectedFirst || keys[1] != expectedSecond {
		t.Fatalf("unexpected key order: %#v", keys)
	}

	next, err := s.ListLocalKeysPage(context.Background(), expectedFirst, 10)
	if err != nil {
		t.Fatalf("list local keys after cursor: %v", err)
	}
	if len(next) != 1 || next[0] != expectedSecond {
		t.Fatalf("unexpected paged keys: %#v", next)
	}
}
