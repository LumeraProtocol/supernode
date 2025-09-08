package cascade

import "time"

// Server-side task envelope timeouts for Cascade service.
//
// Rationale:
//   - RegisterTimeout must exceed the total of SDK upload + processing budgets
//     so the client surfaces errors; currently upload=60m and processing=10m.
//   - Download uses a split approach: a tight server-side preparation timeout
//     and a relaxed client-governed transfer window.
const (
	// RegisterTimeout bounds the entire Register RPC handler lifetime.
	// Must be greater than SDK's upload (60m) + processing (10m) budgets.
	RegisterTimeout = 75 * time.Minute

	// DownloadPrepareTimeout bounds the server-side preparation phase for
	// downloads (fetch metadata, retrieve symbols, reconstruct and verify file).
	// This phase is independent of client bandwidth and should be quick.
	DownloadPrepareTimeout = 20 * time.Minute
)
