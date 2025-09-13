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
	KeySuccessRate      EventDataKey = "success_rate"

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

	// Cascade storage metrics keys
	KeyMetaDurationMS EventDataKey = "meta_duration_ms"
	KeySymDurationMS  EventDataKey = "sym_duration_ms"
	KeyMetaNodes      EventDataKey = "meta_nodes"
	KeySymNodes       EventDataKey = "sym_nodes"

	// Combined store metrics (metadata + symbols)
	KeyStoreDurationMS EventDataKey = "store_duration_ms"
	KeyStoreRequests   EventDataKey = "store_requests"
	KeyStoreCalls      EventDataKey = "store_calls"
	// New minimal store metrics
	KeyStoreSymbolsFirstPass EventDataKey = "store_symbols_first_pass"
	KeyStoreSymbolsTotal     EventDataKey = "store_symbols_total"
	KeyStoreIDFilesCount     EventDataKey = "store_id_files_count"
	KeyStoreCallsByIP        EventDataKey = "store_calls_by_ip"

	// Download (retrieve) detailed metrics
	KeyDHTNodes      EventDataKey = "dht_nodes"
	KeyDHTCalls      EventDataKey = "dht_calls"
	KeyDHTDurationMS EventDataKey = "dht_duration_ms"
	// New minimal download metrics
	KeyRetrieveFoundLocal EventDataKey = "retrieve_found_local"
	KeyRetrieveMS         EventDataKey = "retrieve_ms"
	KeyDecodeMS           EventDataKey = "decode_ms"
	KeyRetrieveCallsByIP  EventDataKey = "retrieve_calls_by_ip"
)
