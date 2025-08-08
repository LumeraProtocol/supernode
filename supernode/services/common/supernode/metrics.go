package supernode

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// MetricsCollector handles system resource monitoring
type MetricsCollector struct{}

// NewMetricsCollector creates a new metrics collector instance
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

// CollectCPUMetrics gathers CPU usage information
// Returns usage percentage as a float64
func (m *MetricsCollector) CollectCPUMetrics(ctx context.Context) (float64, error) {
	percentages, err := cpu.Percent(time.Second, false)
	if err != nil {
		logtrace.Error(ctx, "failed to get cpu info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return 0, err
	}

	return percentages[0], nil
}

// GetCPUCores returns the number of CPU cores
func (m *MetricsCollector) GetCPUCores(ctx context.Context) (int32, error) {
	cores, err := cpu.Counts(true)
	if err != nil {
		logtrace.Error(ctx, "failed to get cpu core count", logtrace.Fields{logtrace.FieldError: err.Error()})
		return 0, err
	}
	
	return int32(cores), nil
}

// CollectMemoryMetrics gathers memory usage information
// Returns memory statistics including total, used, available, and usage percentage
func (m *MetricsCollector) CollectMemoryMetrics(ctx context.Context) (total, used, available uint64, usedPerc float64, err error) {
	vmem, err := mem.VirtualMemory()
	if err != nil {
		logtrace.Error(ctx, "failed to get memory info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return 0, 0, 0, 0, err
	}

	return vmem.Total, vmem.Used, vmem.Available, vmem.UsedPercent, nil
}

// CollectStorageMetrics gathers storage usage information for specified paths
// If paths is empty, it will collect metrics for the root filesystem
func (m *MetricsCollector) CollectStorageMetrics(ctx context.Context, paths []string) []StorageInfo {
	if len(paths) == 0 {
		// Default to root filesystem
		paths = []string{"/"}
	}

	var storageInfos []StorageInfo
	for _, path := range paths {
		usage, err := disk.Usage(path)
		if err != nil {
			logtrace.Error(ctx, "failed to get storage info", logtrace.Fields{
				logtrace.FieldError: err.Error(),
				"path": path,
			})
			continue // Skip this path but continue with others
		}

		storageInfos = append(storageInfos, StorageInfo{
			Path:           path,
			TotalBytes:     usage.Total,
			UsedBytes:      usage.Used,
			AvailableBytes: usage.Free,
			UsagePercent:   usage.UsedPercent,
		})
	}

	return storageInfos
}
