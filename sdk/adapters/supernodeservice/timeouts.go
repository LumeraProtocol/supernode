package supernodeservice

import "time"

// cascadeUploadTimeout provides a generous budget for client-side upload over
// potentially slow networks. Adjust as needed; future work may make this configurable.
const cascadeUploadTimeout = 60 * time.Minute

// cascadeProcessingTimeout bounds the time waiting for server-side processing
// and final response (e.g., tx hash) after upload completes.
const cascadeProcessingTimeout = 20 * time.Minute

// Download timeouts (adapter-level)
//   - downloadPrepIdleTimeout: idle window before the first message arrives,
//     allowing the server time to prepare (e.g., reconstruct large files).
//   - downloadIdleTimeout: cancels if no messages (events/chunks) are received
//     after transfer begins; protects against stalls while allowing long transfers.
//   - downloadMaxTimeout: hard cap for a single download attempt.
const (
	// Give server prep up to ~5m + cushion without client cancelling.
	downloadPrepIdleTimeout = 20 * time.Minute
	downloadIdleTimeout     = 10 * time.Minute
	downloadMaxTimeout      = 60 * time.Minute
)
