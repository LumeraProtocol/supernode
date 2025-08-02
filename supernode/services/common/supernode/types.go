package supernode

// StatusResponse represents the complete system status information
// with clear organization of resources and services
type StatusResponse struct {
	Version            string          // Supernode version
	Resources          Resources       // System resource information
	RunningTasks       []ServiceTasks  // Services with currently running tasks
	RegisteredServices []string        // All registered/available services
}

// Resources contains system resource metrics
type Resources struct {
	CPU     CPUInfo        // CPU usage information
	Memory  MemoryInfo     // Memory usage information
	Storage []StorageInfo  // Storage volumes information
}

// CPUInfo contains CPU usage metrics
type CPUInfo struct {
	UsagePercent float64 // CPU usage percentage (0-100)
}

// MemoryInfo contains memory usage metrics
type MemoryInfo struct {
	TotalBytes     uint64  // Total memory in bytes
	UsedBytes      uint64  // Used memory in bytes
	AvailableBytes uint64  // Available memory in bytes
	UsagePercent   float64 // Memory usage percentage (0-100)
}

// StorageInfo contains storage metrics for a specific path
type StorageInfo struct {
	Path           string  // Storage path being monitored
	TotalBytes     uint64  // Total storage in bytes
	UsedBytes      uint64  // Used storage in bytes
	AvailableBytes uint64  // Available storage in bytes
	UsagePercent   float64 // Storage usage percentage (0-100)
}

// ServiceTasks contains task information for a specific service
type ServiceTasks struct {
	ServiceName string   // Name of the service (e.g., "cascade")
	TaskIDs     []string // List of currently running task IDs
	TaskCount   int32    // Total number of running tasks
}

// TaskProvider interface defines the contract for services to provide
// their running task information to the status service
type TaskProvider interface {
	// GetServiceName returns the unique name identifier for this service
	GetServiceName() string

	// GetRunningTasks returns a list of currently active task IDs
	GetRunningTasks() []string
}
