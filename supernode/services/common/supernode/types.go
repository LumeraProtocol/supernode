package supernode

// StatusResponse represents the complete system status information
// including CPU usage, memory statistics, and service details
type StatusResponse struct {
	CPU struct {
		Usage     string // CPU usage percentage as string (e.g., "45.32")
		Remaining string // Remaining CPU capacity as string (e.g., "54.68")
	}
	Memory struct {
		Total     uint64  // Total memory in bytes
		Used      uint64  // Used memory in bytes
		Available uint64  // Available memory in bytes
		UsedPerc  float64 // Memory usage percentage (0-100)
	}
	RunningTasks       []ServiceTasks // List of services with their running task counts
	RegisteredServices []string       // Names of all registered/available services
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
