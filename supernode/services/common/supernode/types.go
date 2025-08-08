package supernode

// StatusResponse represents the complete system status information
// with clear organization of resources and services
type StatusResponse struct {
	Version            string          // Supernode version
	UptimeSeconds      uint64          // Uptime in seconds
	Resources          Resources       // System resource information
	RunningTasks       []ServiceTasks  // Services with currently running tasks
	RegisteredServices []string        // All registered/available services
	Network            NetworkInfo     // P2P network information
	Rank               int32           // Rank in the top supernodes list (0 if not in top list)
	IPAddress          string          // Supernode IP address with port (e.g., "192.168.1.1:4445")
}

// Resources contains system resource metrics
type Resources struct {
	CPU             CPUInfo        // CPU usage information
	Memory          MemoryInfo     // Memory usage information
	Storage         []StorageInfo  // Storage volumes information
	HardwareSummary string         // Formatted hardware summary (e.g., "8 cores / 32GB RAM")
}

// CPUInfo contains CPU usage metrics
type CPUInfo struct {
	UsagePercent float64 // CPU usage percentage (0-100)
	Cores        int32   // Number of CPU cores
}

// MemoryInfo contains memory usage metrics
type MemoryInfo struct {
	TotalGB      float64 // Total memory in GB
	UsedGB       float64 // Used memory in GB
	AvailableGB  float64 // Available memory in GB
	UsagePercent float64 // Memory usage percentage (0-100)
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

// NetworkInfo contains P2P network information
type NetworkInfo struct {
	PeersCount     int32    // Number of connected peers in P2P network
	PeerAddresses  []string // List of connected peer addresses (optional, may be empty for privacy)
}

// TaskProvider interface defines the contract for services to provide
// their running task information to the status service
type TaskProvider interface {
	// GetServiceName returns the unique name identifier for this service
	GetServiceName() string

	// GetRunningTasks returns a list of currently active task IDs
	GetRunningTasks() []string
}
