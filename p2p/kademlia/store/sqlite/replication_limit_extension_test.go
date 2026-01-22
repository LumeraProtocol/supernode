//go:build !race
// +build !race

package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestGetKeysForReplication_LimitNotHit_ReturnsAllOrdered(t *testing.T) {
	store, err := NewStore(context.Background(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close(context.Background()) })

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustInsertDataRow(t, store, "01", ts)
	mustInsertDataRow(t, store, "00", ts)

	from := ts.Add(-time.Second)
	to := ts.Add(time.Second)
	keys := store.GetKeysForReplication(context.Background(), from, to, 10)
	if keys == nil {
		t.Fatalf("expected non-nil keys")
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0].Key != "00" || keys[1].Key != "01" {
		t.Fatalf("unexpected order: %q, %q", keys[0].Key, keys[1].Key)
	}
}

func TestGetKeysForReplication_LimitHit_NoSameTimestampExtras(t *testing.T) {
	store, err := NewStore(context.Background(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close(context.Background()) })

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := ts.Add(time.Second)

	mustInsertDataRow(t, store, "00", ts)
	mustInsertDataRow(t, store, "01", ts)
	mustInsertDataRow(t, store, "02", ts2)

	from := ts.Add(-time.Second)
	to := ts2.Add(time.Second)
	keys := store.GetKeysForReplication(context.Background(), from, to, 2)
	if keys == nil {
		t.Fatalf("expected non-nil keys")
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0].Key != "00" || keys[1].Key != "01" {
		t.Fatalf("unexpected keys/order: %q, %q", keys[0].Key, keys[1].Key)
	}
}

func TestGetKeysForReplication_LimitHit_IncludesAllSameTimestampKeys(t *testing.T) {
	store, err := NewStore(context.Background(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close(context.Background()) })

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := ts.Add(time.Second)

	// Same timestamp keys. We set maxKeys=2 but expect all keys at the boundary timestamp.
	mustInsertDataRow(t, store, "00", ts)
	mustInsertDataRow(t, store, "01", ts)
	mustInsertDataRow(t, store, "02", ts)
	// Later timestamp key should NOT be included by the timestamp-extension query.
	mustInsertDataRow(t, store, "ff", ts2)

	from := ts.Add(-time.Second)
	to := ts2.Add(time.Second)
	keys := store.GetKeysForReplication(context.Background(), from, to, 2)
	if keys == nil {
		t.Fatalf("expected non-nil keys")
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys (limit extension), got %d", len(keys))
	}
	if keys[0].Key != "00" || keys[1].Key != "01" || keys[2].Key != "02" {
		t.Fatalf("unexpected keys/order: %q, %q, %q", keys[0].Key, keys[1].Key, keys[2].Key)
	}
	if !keys[0].CreatedAt.Equal(ts) || !keys[1].CreatedAt.Equal(ts) || !keys[2].CreatedAt.Equal(ts) {
		t.Fatalf("expected all returned keys to share the boundary createdAt")
	}
}

func mustInsertDataRow(t *testing.T, store *Store, key string, createdAt time.Time) {
	t.Helper()
	// Minimal insert for GetKeysForReplication: only key+createdAt are relevant, but `data` is NOT NULL.
	_, err := store.db.Exec(
		`INSERT INTO data (key, data, is_original, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?)`,
		key,
		[]byte("x"),
		false,
		createdAt,
		createdAt,
	)
	if err != nil {
		t.Fatalf("insert data row: %v", err)
	}
}
