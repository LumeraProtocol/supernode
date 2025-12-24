package reachability

import (
	"sync"
	"sync/atomic"
)

// currentEpochID is a process-wide view of the current chain epoch.
// It is set by the metrics/probing loop and is used by inbound capture points
// to bucket evidence into epochs without requiring them to query chain state.
var currentEpochID atomic.Uint64

// epochInitialized indicates whether the process has ever observed a chain height
// and set a meaningful epoch. This avoids bucketing early inbound evidence into
// epoch 0 before the chain height is available.
var epochInitialized atomic.Bool

func SetCurrentEpochID(epochID uint64) {
	currentEpochID.Store(epochID)
	epochInitialized.Store(true)
}
func CurrentEpochID() uint64 { return currentEpochID.Load() }

// EpochInitialized reports whether SetCurrentEpochID has been called at least once.
func EpochInitialized() bool { return epochInitialized.Load() }

var defaultEpochTracker atomic.Value // *EpochTracker

// SetDefaultEpochTracker sets the process-wide default epoch tracker.
func SetDefaultEpochTracker(t *EpochTracker) { defaultEpochTracker.Store(t) }

// DefaultEpochTracker returns the process-wide default epoch tracker, if set.
func DefaultEpochTracker() *EpochTracker {
	if v := defaultEpochTracker.Load(); v != nil {
		if t, ok := v.(*EpochTracker); ok {
			return t
		}
	}
	return nil
}

// EpochTracker records whether we observed inbound evidence for each service in a given epoch.
// It is intentionally boolean per-service per-epoch (Option A) to keep evidence rules simple.
type EpochTracker struct {
	mu sync.Mutex

	keepEpochs uint64
	seen       map[uint64]map[Service]bool
}

// NewEpochTracker keeps the most recent keepEpochs epochs in memory.
func NewEpochTracker(keepEpochs uint64) *EpochTracker {
	if keepEpochs == 0 {
		keepEpochs = 1
	}
	return &EpochTracker{
		keepEpochs: keepEpochs,
		seen:       make(map[uint64]map[Service]bool),
	}
}

func (t *EpochTracker) Mark(epochID uint64, service Service) {
	if t == nil || service == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	m := t.seen[epochID]
	if m == nil {
		m = make(map[Service]bool, 3)
		t.seen[epochID] = m
	}
	m[service] = true

	t.pruneLocked(epochID)
}

func (t *EpochTracker) Seen(epochID uint64, service Service) bool {
	if t == nil || service == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seen[epochID][service]
}

func (t *EpochTracker) pruneLocked(currentEpoch uint64) {
	if t.keepEpochs == 0 {
		return
	}
	if currentEpoch+1 <= t.keepEpochs {
		return
	}
	minEpoch := currentEpoch - (t.keepEpochs - 1)
	for eid := range t.seen {
		if eid < minEpoch {
			delete(t.seen, eid)
		}
	}
}
