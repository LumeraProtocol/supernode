package task

import (
	"action/event"
	"action/log"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

type TaskEntry struct {
	Task          Task
	TaskID        string
	TaskType      TaskType
	Status        TaskStatus
	Error         error
	Events        []event.Event
	CreatedAt     time.Time
	LastUpdatedAt time.Time
}

type TaskCache struct {
	cache     *ristretto.Cache[string, *TaskEntry]
	logger    log.Logger
	taskLocks sync.Map
}

func NewTaskCache(logger log.Logger) (*TaskCache, error) {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	ctx := context.Background()
	logger.Debug(ctx, "Creating new task cache")

	cache, err := ristretto.NewCache(&ristretto.Config[string, *TaskEntry]{
		NumCounters: 1e4,     // Number of keys to track (10k)
		MaxCost:     1 << 24, // Maximum cost of cache (16MB)
		BufferItems: 64,      // Number of keys per Get buffer
	})
	if err != nil {
		logger.Error(ctx, "Failed to create task cache", "error", err)
		return nil, fmt.Errorf("failed to create task cache: %w", err)
	}

	logger.Info(ctx, "Task cache created successfully")

	return &TaskCache{
		cache:     cache,
		logger:    logger,
		taskLocks: sync.Map{}, // Initialize the mutex map
	}, nil
}

// getOrCreateMutex retrieves or creates a mutex for a specific task ID
func (tc *TaskCache) getOrCreateMutex(taskID string) *sync.Mutex {
	// sync.Map's LoadOrStore is atomic
	mu, _ := tc.taskLocks.LoadOrStore(taskID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// Set stores a task in the cache with initial metadata
func (tc *TaskCache) Set(ctx context.Context, taskID string, task Task, taskType TaskType) bool {
	mu := tc.getOrCreateMutex(taskID)
	mu.Lock()
	defer mu.Unlock()

	tc.logger.Debug(ctx, "Setting task in cache (locked)", "taskID", taskID, "taskType", taskType)

	now := time.Now()
	entry := &TaskEntry{
		Task:          task,
		TaskID:        taskID,
		TaskType:      taskType,
		Status:        StatusPending,
		Events:        make([]event.Event, 0),
		CreatedAt:     now,
		LastUpdatedAt: now,
	}

	success := tc.cache.Set(taskID, entry, 1)
	if !success {
		tc.logger.Warn(ctx, "Failed to set task in cache", "taskID", taskID)
	}
	return success
}

// Get retrieves a task entry from the cache
func (tc *TaskCache) Get(ctx context.Context, taskID string) (*TaskEntry, bool) {
	tc.logger.Debug(ctx, "Getting task from cache", "taskID", taskID)
	entry, found := tc.cache.Get(taskID)
	if !found {
		tc.logger.Debug(ctx, "Task not found in cache", "taskID", taskID)
	}
	return entry, found
}

// UpdateStatus updates the status of a task in the cache atomically
func (tc *TaskCache) UpdateStatus(ctx context.Context, taskID string, status TaskStatus, err error) bool {
	mu := tc.getOrCreateMutex(taskID)
	mu.Lock()
	defer mu.Unlock()

	tc.logger.Debug(ctx, "Updating task status (locked)", "taskID", taskID, "status", status)

	// Perform Get-Modify-Set within the lock
	existingEntry, found := tc.cache.Get(taskID)
	if !found {
		tc.logger.Warn(ctx, "Cannot update status - task not found (locked)", "taskID", taskID)
		return false // Task doesn't exist
	}

	// Create a new entry with updated status
	updatedEntry := *existingEntry // Copy the struct
	updatedEntry.Status = status
	updatedEntry.Error = err
	updatedEntry.LastUpdatedAt = time.Now()

	// Set the modified entry back into the cache
	success := tc.cache.Set(taskID, &updatedEntry, 1)
	if !success {
		tc.logger.Warn(ctx, "Failed to update status in cache (locked)", "taskID", taskID)
	}
	return success
}

// AddEvent adds an event to the task's event history atomically
func (tc *TaskCache) AddEvent(ctx context.Context, taskID string, e event.Event) bool {
	mu := tc.getOrCreateMutex(taskID)
	mu.Lock()
	defer mu.Unlock()

	tc.logger.Debug(ctx, "Adding event to task (locked)", "taskID", taskID, "eventType", e.Type)

	// Perform Get-Modify-Set within the lock
	existingEntry, found := tc.cache.Get(taskID)
	if !found {
		tc.logger.Warn(ctx, "Cannot add event - task not found (locked)", "taskID", taskID)
		return false // Task doesn't exist
	}

	// Create a new entry with the event added
	updatedEntry := *existingEntry // Copy the struct

	// Ensure we modify a copy of the slice
	updatedEvents := make([]event.Event, len(existingEntry.Events), len(existingEntry.Events)+1)
	copy(updatedEvents, existingEntry.Events)
	updatedEvents = append(updatedEvents, e)

	updatedEntry.Events = updatedEvents
	updatedEntry.LastUpdatedAt = time.Now()

	// Set the modified entry back into the cache
	success := tc.cache.Set(taskID, &updatedEntry, 1)
	if !success {
		tc.logger.Warn(ctx, "Failed to add event in cache (locked)", "taskID", taskID)
	}
	return success
}

// Wait waits for all operations to complete
func (tc *TaskCache) Wait() {
	ctx := context.Background()
	tc.logger.Debug(ctx, "Waiting for cache operations to complete")
	tc.cache.Wait()
}

// Close cleans up resources
func (tc *TaskCache) Close() {
	ctx := context.Background()
	tc.logger.Debug(ctx, "Closing task cache")
	if tc.cache != nil {
		tc.cache.Close()
	}
}

// Del deletes a task and its associated lock
func (tc *TaskCache) Del(ctx context.Context, taskID string) {
	mu := tc.getOrCreateMutex(taskID)
	mu.Lock()
	defer mu.Unlock()

	tc.logger.Debug(ctx, "Deleting task from cache (locked)", "taskID", taskID)
	tc.cache.Del(taskID)
	tc.taskLocks.Delete(taskID) // Remove the mutex from the map to prevent memory leaks
}
