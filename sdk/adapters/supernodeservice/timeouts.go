package supernodeservice

import "time"

// cascadeUploadTimeout provides a generous budget for client-side upload over
// potentially slow networks. Adjust as needed; future work may make this configurable.
const cascadeUploadTimeout = 60 * time.Minute

// cascadeProcessingTimeout bounds the time waiting for server-side processing
// and final response (e.g., tx hash) after upload completes.
const cascadeProcessingTimeout = 10 * time.Minute
