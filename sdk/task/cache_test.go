package task

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"
	"github.com/stretchr/testify/assert"
)

func newTestCache(t *testing.T) *TaskCache {
	t.Helper()

	tc, err := NewTaskCache(context.Background(), log.NewNoopLogger())
	if err != nil {
		t.Fatalf("creating cache: %v", err)
	}
	return tc
}

func TestTaskCache_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	tc := newTestCache(t)
	tc.Set(ctx, "conc", nil, TaskTypeCascade)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			tc.UpdateStatus(ctx, "conc", StatusProcessing, nil)
			tc.AddEvent(ctx, "conc", event.Event{Type: event.TaskStarted})
		}
	}()

	go func() {
		defer wg.Done()
		tc.Del(ctx, "conc") // race with updates
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent operations deadlocked")
	}
}

func TestTaskCache_SetGet(t *testing.T) {
	ctx := context.Background()
	tc := newTestCache(t)

	tests := map[string]struct {
		taskID   string
		taskType TaskType
	}{
		"first insert":  {"id1", TaskTypeCascade},
		"second insert": {"id2", TaskTypeCascade},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tc.Set(ctx, tt.taskID, nil, tt.taskType)
			tc.Wait() // <-- ensure write propagated

			entry, found := tc.Get(ctx, tt.taskID)
			assert.True(t, found, "entry should exist")
			assert.Equal(t, tt.taskID, entry.TaskID)
			assert.Equal(t, StatusPending, entry.Status)
		})
	}
}

func TestTaskCache_UpdateStatus(t *testing.T) {
	ctx := context.Background()
	tc := newTestCache(t)

	tc.Set(ctx, "jobX", nil, TaskTypeCascade)
	tc.Wait()

	tc.UpdateStatus(ctx, "jobX", StatusProcessing, nil)
	tc.Wait()

	ent, _ := tc.Get(ctx, "jobX")
	assert.Equal(t, StatusProcessing, ent.Status)
}

func TestTaskCache_AddEvent(t *testing.T) {
	ctx := context.Background()
	tc := newTestCache(t)

	tc.Set(ctx, "evt1", nil, TaskTypeCascade)
	tc.Wait()

	ev := event.Event{Type: event.TaskStarted, TaskID: "evt1"}
	tc.AddEvent(ctx, "evt1", ev)
	tc.Wait()

	ent, _ := tc.Get(ctx, "evt1")
	assert.Len(t, ent.Events, 1)
	assert.Equal(t, ev, ent.Events[0])
}

func TestTaskCache_Delete(t *testing.T) {
	ctx := context.Background()
	tc := newTestCache(t)

	tc.Set(ctx, "gone", nil, TaskTypeCascade)
	tc.Wait()

	tc.Del(ctx, "gone")
	tc.Wait()

	_, found := tc.Get(ctx, "gone")
	assert.False(t, found)
}
