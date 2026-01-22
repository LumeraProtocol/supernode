//go:build !race
// +build !race

package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestGetKeysForReplication_CanceledContext(t *testing.T) {
	store, err := NewStore(context.Background(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	keys := store.GetKeysForReplication(ctx, time.Now().Add(-time.Hour), time.Now(), 10)
	if keys != nil {
		t.Fatalf("expected nil on canceled context, got len=%d", len(keys))
	}
}
