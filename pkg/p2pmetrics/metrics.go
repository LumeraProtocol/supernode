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
	mu               sync.Mutex
	CallsByIP        map[string][]Call
	SymbolsFirstPass int
	SymbolsTotal     int
	IDFilesCount     int
	DurationMS       int64
}

// storeSessions holds per-task store sessions safely for concurrent access.
var storeSessions sync.Map // map[string]*storeSession

// RegisterStoreBridge hooks store callbacks into the store session collector.
func StartStoreCapture(taskID string) {
	RegisterStoreHook(taskID, func(c Call) {
		if taskID == "" {
			return
		}
		sAny, _ := storeSessions.LoadOrStore(taskID, &storeSession{CallsByIP: map[string][]Call{}})
		s := sAny.(*storeSession)
		key := c.IP
		if key == "" {
			key = c.Address
		}
		s.mu.Lock()
		s.CallsByIP[key] = append(s.CallsByIP[key], c)
		s.mu.Unlock()
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
	sAny, _ := storeSessions.LoadOrStore(taskID, &storeSession{CallsByIP: map[string][]Call{}})
	s := sAny.(*storeSession)
	s.mu.Lock()
	s.SymbolsFirstPass = symbolsFirstPass
	s.SymbolsTotal = symbolsTotal
	s.IDFilesCount = idFilesCount
	s.DurationMS = durationMS
	s.mu.Unlock()
}

// BuildStoreEventPayloadFromCollector builds the store event payload (minimal).
func BuildStoreEventPayloadFromCollector(taskID string) map[string]any {
	sAny, ok := storeSessions.Load(taskID)
	if !ok {
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
	s := sAny.(*storeSession)
	// Snapshot under lock to avoid races while building payload
	s.mu.Lock()
	callsCopy := make(map[string][]Call, len(s.CallsByIP))
	for k, v := range s.CallsByIP {
		// copy slice to avoid exposing internal backing array
		vv := make([]Call, len(v))
		copy(vv, v)
		callsCopy[k] = vv
	}
	duration := s.DurationMS
	first := s.SymbolsFirstPass
	total := s.SymbolsTotal
	ids := s.IDFilesCount
	s.mu.Unlock()
	// Compute per-call success rate across first-pass store RPC attempts
	totalCalls := 0
	successCalls := 0
	for _, calls := range callsCopy {
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
			"duration_ms":        duration,
			"symbols_first_pass": first,
			"symbols_total":      total,
			"id_files_count":     ids,
			"success_rate_pct":   successRate,
			"calls_by_ip":        callsCopy,
		},
	}
}

// Retrieve session
type retrieveSession struct {
	mu         sync.Mutex
	CallsByIP  map[string][]Call
	FoundLocal int
	RetrieveMS int64
	DecodeMS   int64
}

var retrieveSessions sync.Map // map[string]*retrieveSession

// RegisterRetrieveBridge hooks retrieve callbacks into the retrieve collector.
func StartRetrieveCapture(taskID string) {
	RegisterRetrieveHook(taskID, func(c Call) {
		if taskID == "" {
			return
		}
		sAny, _ := retrieveSessions.LoadOrStore(taskID, &retrieveSession{CallsByIP: map[string][]Call{}})
		s := sAny.(*retrieveSession)
		key := c.IP
		if key == "" {
			key = c.Address
		}
		s.mu.Lock()
		s.CallsByIP[key] = append(s.CallsByIP[key], c)
		s.mu.Unlock()
	})
	RegisterFoundLocalHook(taskID, func(n int) {
		if taskID == "" {
			return
		}
		sAny, _ := retrieveSessions.LoadOrStore(taskID, &retrieveSession{CallsByIP: map[string][]Call{}})
		s := sAny.(*retrieveSession)
		s.mu.Lock()
		s.FoundLocal = n
		s.mu.Unlock()
	})
}

func StopRetrieveCapture(taskID string) {
	UnregisterRetrieveHook(taskID)
	UnregisterFoundLocalHook(taskID)
}

// SetRetrieveSummary sets timing info for retrieve/decode phases.
func SetRetrieveSummary(taskID string, retrieveMS, decodeMS int64) {
	if taskID == "" {
		return
	}
	sAny, _ := retrieveSessions.LoadOrStore(taskID, &retrieveSession{CallsByIP: map[string][]Call{}})
	s := sAny.(*retrieveSession)
	s.mu.Lock()
	s.RetrieveMS = retrieveMS
	s.DecodeMS = decodeMS
	s.mu.Unlock()
}

// BuildDownloadEventPayloadFromCollector builds the download section payload.
func BuildDownloadEventPayloadFromCollector(taskID string) map[string]any {
	sAny, ok := retrieveSessions.Load(taskID)
	if !ok {
		return map[string]any{
			"retrieve": map[string]any{
				"found_local": 0,
				"retrieve_ms": int64(0),
				"decode_ms":   int64(0),
				"calls_by_ip": map[string][]Call{},
			},
		}
	}
	s := sAny.(*retrieveSession)
	s.mu.Lock()
	callsCopy := make(map[string][]Call, len(s.CallsByIP))
	for k, v := range s.CallsByIP {
		vv := make([]Call, len(v))
		copy(vv, v)
		callsCopy[k] = vv
	}
	found := s.FoundLocal
	rMS := s.RetrieveMS
	dMS := s.DecodeMS
	s.mu.Unlock()
	return map[string]any{
		"retrieve": map[string]any{
			"found_local": found,
			"retrieve_ms": rMS,
			"decode_ms":   dMS,
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
