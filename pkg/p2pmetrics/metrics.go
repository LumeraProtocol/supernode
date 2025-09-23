package p2pmetrics

import (
	"context"
	"sync"
)

// Call represents a single per-node RPC outcome (store or retrieve).
type Call struct {
	IP         string `json:"ip"`
	Address    string `json:"address"`
	Keys       int    `json:"keys"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Noop       bool   `json:"noop,omitempty"`
}

// -------- Lightweight hooks  -------------------------

var (
	storeMu   sync.RWMutex
	storeHook = make(map[string]func(Call))

	retrieveMu   sync.RWMutex
	retrieveHook = make(map[string]func(Call))

	foundLocalMu sync.RWMutex
	foundLocalCb = make(map[string]func(int))
)

// RegisterStoreHook registers a callback to receive store RPC calls for a task.
func RegisterStoreHook(taskID string, fn func(Call)) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if fn == nil {
		delete(storeHook, taskID)
		return
	}
	storeHook[taskID] = fn
}

// UnregisterStoreHook removes the registered store callback for a task.
func UnregisterStoreHook(taskID string) { RegisterStoreHook(taskID, nil) }

// RecordStore invokes the registered store callback for the given task, if any.
func RecordStore(taskID string, c Call) {
	storeMu.RLock()
	fn := storeHook[taskID]
	storeMu.RUnlock()
	if fn != nil {
		fn(c)
	}
}

// RegisterRetrieveHook registers a callback to receive retrieve RPC calls.
func RegisterRetrieveHook(taskID string, fn func(Call)) {
	retrieveMu.Lock()
	defer retrieveMu.Unlock()
	if fn == nil {
		delete(retrieveHook, taskID)
		return
	}
	retrieveHook[taskID] = fn
}

// UnregisterRetrieveHook removes the registered retrieve callback for a task.
func UnregisterRetrieveHook(taskID string) { RegisterRetrieveHook(taskID, nil) }

// RecordRetrieve invokes the registered retrieve callback for the given task.
func RecordRetrieve(taskID string, c Call) {
	retrieveMu.RLock()
	fn := retrieveHook[taskID]
	retrieveMu.RUnlock()
	if fn != nil {
		fn(c)
	}
}

// RegisterFoundLocalHook registers a callback to receive found-local counts.
func RegisterFoundLocalHook(taskID string, fn func(int)) {
	foundLocalMu.Lock()
	defer foundLocalMu.Unlock()
	if fn == nil {
		delete(foundLocalCb, taskID)
		return
	}
	foundLocalCb[taskID] = fn
}

// UnregisterFoundLocalHook removes the registered found-local callback.
func UnregisterFoundLocalHook(taskID string) { RegisterFoundLocalHook(taskID, nil) }

// ReportFoundLocal invokes the registered found-local callback for the task.
func ReportFoundLocal(taskID string, count int) {
	foundLocalMu.RLock()
	fn := foundLocalCb[taskID]
	foundLocalMu.RUnlock()
	if fn != nil {
		fn(count)
	}
}

// -------- Minimal in-process collectors for events --------------------------

// Store session
type storeSession struct {
	CallsByIP        map[string][]Call
	SymbolsFirstPass int
	SymbolsTotal     int
	IDFilesCount     int
	DurationMS       int64
}

var storeSessions = struct{ m map[string]*storeSession }{m: map[string]*storeSession{}}

// RegisterStoreBridge hooks store callbacks into the store session collector.
func StartStoreCapture(taskID string) {
	RegisterStoreHook(taskID, func(c Call) {
		s := storeSessions.m[taskID]
		if s == nil {
			s = &storeSession{CallsByIP: map[string][]Call{}}
			storeSessions.m[taskID] = s
		}
		key := c.IP
		if key == "" {
			key = c.Address
		}
		s.CallsByIP[key] = append(s.CallsByIP[key], c)
	})
}

func StopStoreCapture(taskID string) { UnregisterStoreHook(taskID) }

// SetStoreSummary sets store summary fields for the first pass and totals.
//
// - symbolsFirstPass: number of symbols sent during the first pass
// - symbolsTotal: total symbols available in the directory
// - idFilesCount: number of ID/metadata files included in the first combined batch
// - durationMS: elapsed time of the first-pass store phase
func SetStoreSummary(taskID string, symbolsFirstPass, symbolsTotal, idFilesCount int, durationMS int64) {
	if taskID == "" {
		return
	}
	s := storeSessions.m[taskID]
	if s == nil {
		s = &storeSession{CallsByIP: map[string][]Call{}}
		storeSessions.m[taskID] = s
	}
	s.SymbolsFirstPass = symbolsFirstPass
	s.SymbolsTotal = symbolsTotal
	s.IDFilesCount = idFilesCount
	s.DurationMS = durationMS
}

// BuildStoreEventPayloadFromCollector builds the store event payload (minimal).
func BuildStoreEventPayloadFromCollector(taskID string) map[string]any {
	s := storeSessions.m[taskID]
	if s == nil {
		return map[string]any{
			"store": map[string]any{
				"duration_ms":        int64(0),
				"symbols_first_pass": 0,
				"symbols_total":      0,
				"id_files_count":     0,
				"success_rate_pct":   float64(0),
				"calls_by_ip":        map[string][]Call{},
			},
		}
	}
	// Compute per-call success rate across first-pass store RPC attempts
	totalCalls := 0
	successCalls := 0
	for _, calls := range s.CallsByIP {
		for _, c := range calls {
			totalCalls++
			if c.Success {
				successCalls++
			}
		}
	}
	var successRate float64
	if totalCalls > 0 {
		successRate = float64(successCalls) / float64(totalCalls) * 100.0
	}
	return map[string]any{
		"store": map[string]any{
			"duration_ms":        s.DurationMS,
			"symbols_first_pass": s.SymbolsFirstPass,
			"symbols_total":      s.SymbolsTotal,
			"id_files_count":     s.IDFilesCount,
			"success_rate_pct":   successRate,
			"calls_by_ip":        s.CallsByIP,
		},
	}
}

// Retrieve session
type retrieveSession struct {
	mu         sync.RWMutex
	CallsByIP  map[string][]Call
	FoundLocal int
	FoundNet   int
	Keys       int
	Required   int
	RetrieveMS int64
	DecodeMS   int64
}

var retrieveSessions = struct{ m map[string]*retrieveSession }{m: map[string]*retrieveSession{}}

// internal event channel for retrieve metrics (per task)
type retrieveEvent struct {
	typ  int // 0: per-node call, 1: found-local update
	call Call
	n    int
}

var retrieveEventChans = struct {
	mu sync.Mutex
	m  map[string]chan retrieveEvent
}{m: map[string]chan retrieveEvent{}}

// StartRetrieveCapture hooks retrieve callbacks into a buffered channel and a
// single goroutine that serializes updates to avoid concurrent map writes.
func StartRetrieveCapture(taskID string) {
	// Create or get session upfront
	s := retrieveSessions.m[taskID]
	if s == nil {
		s = &retrieveSession{CallsByIP: map[string][]Call{}}
		retrieveSessions.m[taskID] = s
	}

	// Per-task buffered channel
	ch := make(chan retrieveEvent, 4096)
	retrieveEventChans.mu.Lock()
	retrieveEventChans.m[taskID] = ch
	retrieveEventChans.mu.Unlock()

	// Worker goroutine to serialize writes
	go func(taskID string, ch <-chan retrieveEvent) {
		for ev := range ch {
			sess := retrieveSessions.m[taskID]
			if sess == nil {
				sess = &retrieveSession{CallsByIP: map[string][]Call{}}
				retrieveSessions.m[taskID] = sess
			}
			switch ev.typ {
			case 0: // per-node call
				key := ev.call.IP
				if key == "" {
					key = ev.call.Address
				}
				sess.mu.Lock()
				if sess.CallsByIP == nil {
					sess.CallsByIP = map[string][]Call{}
				}
				sess.CallsByIP[key] = append(sess.CallsByIP[key], ev.call)
				sess.mu.Unlock()
			case 1: // found-local update
				sess.FoundLocal = ev.n
			}
		}
	}(taskID, ch)

	// Register hooks that enqueue events (non-blocking)
	RegisterRetrieveHook(taskID, func(c Call) {
		retrieveEventChans.mu.Lock()
		ch, ok := retrieveEventChans.m[taskID]
		retrieveEventChans.mu.Unlock()
		if ok {
			select {
			case ch <- retrieveEvent{typ: 0, call: c}:
			default: // drop if buffer is full
			}
		}
	})
	RegisterFoundLocalHook(taskID, func(n int) {
		retrieveEventChans.mu.Lock()
		ch, ok := retrieveEventChans.m[taskID]
		retrieveEventChans.mu.Unlock()
		if ok {
			select {
			case ch <- retrieveEvent{typ: 1, n: n}:
			default:
			}
		}
	})
}

func StopRetrieveCapture(taskID string) {
	UnregisterRetrieveHook(taskID)
	UnregisterFoundLocalHook(taskID)
	retrieveEventChans.mu.Lock()
	if ch, ok := retrieveEventChans.m[taskID]; ok {
		delete(retrieveEventChans.m, taskID)
		close(ch)
	}
	retrieveEventChans.mu.Unlock()
}

// SetRetrieveBatchSummary sets counts for a retrieval attempt.
func SetRetrieveBatchSummary(taskID string, keys, required, foundLocal, foundNet int, retrieveMS int64) {
	if taskID == "" {
		return
	}
	s := retrieveSessions.m[taskID]
	if s == nil {
		s = &retrieveSession{CallsByIP: map[string][]Call{}}
		retrieveSessions.m[taskID] = s
	}
	s.Keys = keys
	s.Required = required
	s.FoundLocal = foundLocal
	s.FoundNet = foundNet
	s.RetrieveMS = retrieveMS
}

// SetRetrieveSummary sets timing info for retrieve/decode phases.
func SetRetrieveSummary(taskID string, retrieveMS, decodeMS int64) {
	if taskID == "" {
		return
	}
	s := retrieveSessions.m[taskID]
	if s == nil {
		s = &retrieveSession{CallsByIP: map[string][]Call{}}
		retrieveSessions.m[taskID] = s
	}
	s.RetrieveMS = retrieveMS
	s.DecodeMS = decodeMS
}

// BuildDownloadEventPayloadFromCollector builds the download section payload.
func BuildDownloadEventPayloadFromCollector(taskID string) map[string]any {
	s := retrieveSessions.m[taskID]
	if s == nil {
		return map[string]any{
			"retrieve": map[string]any{
				"keys":        0,
				"required":    0,
				"found_local": 0,
				"found_net":   0,
				"retrieve_ms": int64(0),
				"decode_ms":   int64(0),
				"calls_by_ip": map[string][]Call{},
			},
		}
	}
	// Create a snapshot copy of CallsByIP to avoid concurrent map access
	s.mu.RLock()
	callsCopy := make(map[string][]Call, len(s.CallsByIP))
	for k, v := range s.CallsByIP {
		vv := make([]Call, len(v))
		copy(vv, v)
		callsCopy[k] = vv
	}
	s.mu.RUnlock()

	return map[string]any{
		"retrieve": map[string]any{
			"keys":        s.Keys,
			"required":    s.Required,
			"found_local": s.FoundLocal,
			"found_net":   s.FoundNet,
			"retrieve_ms": s.RetrieveMS,
			"decode_ms":   s.DecodeMS,
			"calls_by_ip": callsCopy,
		},
	}
}

// -------- Context helpers (dedicated to metrics tagging) --------------------

type ctxKey string

var taskIDKey ctxKey = "p2pmetrics-task-id"

// WithTaskID returns a child context with the metrics task ID set.
func WithTaskID(ctx context.Context, taskID string) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithValue(ctx, taskIDKey, taskID)
}

// TaskIDFromContext extracts the metrics task ID from context (or "").
func TaskIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(taskIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
