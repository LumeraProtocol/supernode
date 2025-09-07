package task

import "time"

// connectionTimeout bounds supernode health/connection probing.
// Keep this short to preserve snappy discovery without impacting long uploads.
const connectionTimeout = 5 * time.Second
