package task

import "time"

// Connection and health check timeouts
const (
	// connectionTimeout bounds supernode health/connection probing.
	// Keep this short to preserve snappy discovery without impacting long uploads.
	connectionTimeout = 10 * time.Second
)

// Task execution timeouts
const (
	// downloadTimeout bounds a single download attempt at the task layer.
	// This should exceed typical slow-client scenarios; fine-grained
	// liveness is enforced by the adapter via an idle timeout.
	downloadTimeout = 60 * time.Minute
)

// Note: Upload and processing timeouts are defined in sdk/adapters/supernodeservice/timeouts.go
// as they are specific to the adapter implementation:
// - cascadeUploadTimeout = 60 * time.Minute (for slow network uploads)
// - cascadeProcessingTimeout = 10 * time.Minute (for server-side processing)
