package supernode

// StatusResponse represents the complete system status information
// with clear organization of resources and services
type StatusResponse struct {
	Version            string      // Supernode version
	UptimeSeconds      uint64      // Uptime in seconds
	Resources          Resources   // System resource information
	RegisteredServices []string    // All registered/available services
	Network            NetworkInfo // P2P network information
	Rank               int32       // Rank in the top supernodes list (0 if not in top list)
	IPAddress          string      // Supernode IP address with port (e.g., "192.168.1.1:4445")
	P2PMetrics         P2PMetrics  // Detailed P2P metrics snapshot
}

// Resources contains system resource metrics
type Resources struct {
	CPU             CPUInfo       // CPU usage information
	Memory          MemoryInfo    // Memory usage information
	Storage         []StorageInfo // Storage volumes information
	HardwareSummary string        // Formatted hardware summary (e.g., "8 cores / 32GB RAM")
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

// NetworkInfo contains P2P network information
type NetworkInfo struct {
	PeersCount    int32    // Number of connected peers in P2P network
	PeerAddresses []string // List of connected peer addresses (optional, may be empty for privacy)
}

// P2PMetrics mirrors the proto P2P metrics for status API
type P2PMetrics struct {
	DhtMetrics           DhtMetrics
	NetworkHandleMetrics map[string]HandleCounters
	ConnPoolMetrics      map[string]int64
	BanList              []BanEntry
	Database             DatabaseStats
	Disk                 DiskStatus
}

type StoreSuccessPoint struct {
	TimeUnix    int64
	Requests    int32
	Successful  int32
	SuccessRate float64
}

type BatchRetrievePoint struct {
	TimeUnix     int64
	Keys         int32
	Required     int32
	FoundLocal   int32
	FoundNetwork int32
	DurationMS   int64
}

type DhtMetrics struct {
	StoreSuccessRecent   []StoreSuccessPoint
	BatchRetrieveRecent  []BatchRetrievePoint
	HotPathBannedSkips   int64
	HotPathBanIncrements int64
}

type HandleCounters struct {
	Total   int64
	Success int64
	Failure int64
	Timeout int64
}

type BanEntry struct {
	ID            string
	IP            string
	Port          uint32
	Count         int32
	CreatedAtUnix int64
	AgeSeconds    int64
}

type DatabaseStats struct {
	P2PDBSizeMB       float64
	P2PDBRecordsCount int64
}

type DiskStatus struct {
	AllMB  float64
	UsedMB float64
	FreeMB float64
}
