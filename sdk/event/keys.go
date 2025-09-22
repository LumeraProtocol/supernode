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

	// Removed legacy cascade storage metrics keys (meta/sym timings and nodes)

	// Combined store metrics (metadata + symbols) — new minimal only
	KeyStoreDurationMS EventDataKey = "store_duration_ms"
	// New minimal store metrics
	KeyStoreSymbolsFirstPass EventDataKey = "store_symbols_first_pass"
	KeyStoreSymbolsTotal     EventDataKey = "store_symbols_total"
	KeyStoreIDFilesCount     EventDataKey = "store_id_files_count"
	KeyStoreCallsByIP        EventDataKey = "store_calls_by_ip"

	// Download (retrieve) detailed metrics — new minimal only
	KeyRetrieveFoundLocal EventDataKey = "retrieve_found_local"
	KeyRetrieveMS         EventDataKey = "retrieve_ms"
	KeyDecodeMS           EventDataKey = "decode_ms"
	KeyRetrieveCallsByIP  EventDataKey = "retrieve_calls_by_ip"
	// Additional retrieve summary fields
	KeyRetrieveKeys          EventDataKey = "retrieve_keys"
	KeyRetrieveRequired      EventDataKey = "retrieve_required"
	KeyRetrieveFoundNet      EventDataKey = "retrieve_found_net"
	KeyTargetRequiredPercent EventDataKey = "target_required_percent"
	KeyTargetRequiredCount   EventDataKey = "target_required_count"
	KeyTotalSymbols          EventDataKey = "total_symbols"
)
