package event

// EventDataKey defines standard keys used in event data
type EventDataKey string

const (
	// Common data keys
	KeyError            EventDataKey = "error"
	KeyCount            EventDataKey = "count"
	KeySupernode        EventDataKey = "supernode"
	KeySupernodeAddress EventDataKey = "sn-address"
	KeyIteration        EventDataKey = "iteration"
	KeyTxHash           EventDataKey = "txhash"
	KeyMessage          EventDataKey = "message"
	KeyProgress         EventDataKey = "progress"
	KeyEventType        EventDataKey = "event_type"
	KeyOutputPath       EventDataKey = "output_path"

	// Upload/download metrics keys (no progress events; start/complete metrics only)
	KeyBytesTotal     EventDataKey = "bytes_total"
	KeyChunkSize      EventDataKey = "chunk_size"
	KeyEstChunks      EventDataKey = "est_chunks"
	KeyChunks         EventDataKey = "chunks"
	KeyElapsedSeconds EventDataKey = "elapsed_seconds"
	KeyThroughputMBS  EventDataKey = "throughput_mb_s"
	KeyChunkIndex     EventDataKey = "chunk_index"
	KeyReason         EventDataKey = "reason"

	// Task specific keys
	KeyTaskID   EventDataKey = "task_id"
	KeyActionID EventDataKey = "action_id"

    // Removed legacy cascade storage/retrieve metrics keys
)
