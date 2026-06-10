package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStore_CloseStopsWorkersAndIsIdempotent verifies the store shutdown
// contract that backs TestGetKeysForReplication_CanceledContext's TempDir
// cleanup: Close must stop every background goroutine (DB worker, checkpoint
// worker, replication writer) and wait for them to exit, so nothing touches the
// database or its WAL files afterwards. It must also be safe to call more than
// once.
//
// Regression guard: Worker.Stop previously sent a single value on an unbuffered
// quit channel that two goroutines selected on, so the checkpoint worker could
// leak and keep writing WAL/-shm files after Close returned — racing teardown.
func TestStore_CloseStopsWorkersAndIsIdempotent(t *testing.T) {
	store, err := NewStore(context.Background(), t.TempDir(), nil, nil)
	require.NoError(t, err)

	// Close must return promptly: if a worker ignored the quit signal, the
	// internal WaitGroup would block here forever.
	done := make(chan struct{})
	go func() {
		store.Close(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not return in time; a background worker did not stop")
	}

	// Idempotent: a second Close must neither panic (double close of quit
	// channels) nor hang.
	secondClose := make(chan struct{})
	go func() {
		require.NotPanics(t, func() { store.Close(context.Background()) })
		close(secondClose)
	}()
	select {
	case <-secondClose:
	case <-time.After(5 * time.Second):
		t.Fatal("second Close did not return in time")
	}
}
