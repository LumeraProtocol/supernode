// Package task provides a lean, concurrency-safe, in-memory tracker for
// live tasks running inside a service. It is designed to be generic and
// reusable across multiple features (e.g., cascade upload/download) and
// only tracks tasks while the enclosing RPC/handler is alive. No
// persistence, progress reporting, or background processing is included.
package task

import "sync"

// Tracker defines a minimal interface for tracking live tasks per service.
// Implementations must be concurrency-safe. All methods are non-blocking
// and best-effort; invalid inputs are ignored.
type Tracker interface {
	Start(service, taskID string)
	End(service, taskID string)
	Snapshot() map[string][]string
}

// InMemoryTracker is a lean, concurrency-safe tracker of live tasks.
// It stores only in-memory state for the lifetime of the process and
// returns copies when asked for a snapshot to ensure isolation.
type InMemoryTracker struct {
	mu sync.RWMutex
	// service -> set(taskID)
	data map[string]map[string]struct{}
}

// New creates and returns a new in-memory tracker.
func New() *InMemoryTracker {
	return &InMemoryTracker{data: make(map[string]map[string]struct{})}
}

// TryStart attempts to mark a task as running under a given service.
// It returns true if the task was newly started, or false if it was already running
// (or if inputs are invalid). This is useful for "only one in-flight task" guards.
func (t *InMemoryTracker) TryStart(service, taskID string) bool {
	if service == "" || taskID == "" {
		return false
	}
	t.mu.Lock()
	m, ok := t.data[service]
	if !ok {
		m = make(map[string]struct{})
		t.data[service] = m
	}
	if _, exists := m[taskID]; exists {
		t.mu.Unlock()
		return false
	}
	m[taskID] = struct{}{}
	t.mu.Unlock()
	return true
}

// Start marks a task as running under a given service. Empty arguments
// are ignored. Calling Start with the same (service, taskID) pair is idempotent.
func (t *InMemoryTracker) Start(service, taskID string) {
	if service == "" || taskID == "" {
		return
	}
	t.mu.Lock()
	m, ok := t.data[service]
	if !ok {
		m = make(map[string]struct{})
		t.data[service] = m
	}
	m[taskID] = struct{}{}
	t.mu.Unlock()
}

// End removes a running task under a given service. Empty arguments
// are ignored. Removing a non-existent (service, taskID) pair is a no-op.
func (t *InMemoryTracker) End(service, taskID string) {
	if service == "" || taskID == "" {
		return
	}
	t.mu.Lock()
	if m, ok := t.data[service]; ok {
		delete(m, taskID)
		if len(m) == 0 {
			delete(t.data, service)
		}
	}
	t.mu.Unlock()
}

// Snapshot returns a copy of the current running tasks per service.
// The returned map and slices are independent of internal state.
func (t *InMemoryTracker) Snapshot() map[string][]string {
	out := make(map[string][]string)
	t.mu.RLock()
	for svc, m := range t.data {
		ids := make([]string, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		out[svc] = ids
	}
	t.mu.RUnlock()
	return out
}
