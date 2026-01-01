package status

import (
	"context"
	"math"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// diskSizeAdjustFactor compensates for observed discrepancies between the disk
// size reported by the node runtime and the "expected" decimal-GB figure used by
// external consumers (dashboards/on-chain metrics).
//
// Rationale:
//   - Keep the adjustment in exactly one place (the status metrics source) so all
//     downstream consumers remain consistent.
//   - Apply it to total only to match the expected decimal-GB figure while
//     leaving free as reported by the runtime.
const diskSizeAdjustFactor = 1.1

func adjustDiskBytes(value uint64) uint64 {
	return uint64(math.Round(float64(value) * diskSizeAdjustFactor))
}

// MetricsCollector handles system resource monitoring
type MetricsCollector struct{}

// NewMetricsCollector creates a new metrics collector instance
func NewMetricsCollector() *MetricsCollector { return &MetricsCollector{} }

// CollectCPUMetrics gathers CPU usage information
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
func (m *MetricsCollector) CollectMemoryMetrics(ctx context.Context) (total, used, available uint64, usedPerc float64, err error) {
	vmem, err := mem.VirtualMemory()
	if err != nil {
		logtrace.Error(ctx, "failed to get memory info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return 0, 0, 0, 0, err
	}
	return vmem.Total, vmem.Used, vmem.Available, vmem.UsedPercent, nil
}

// StorageInfo holds disk usage stats
type StorageInfo struct {
	Path           string
	TotalBytes     uint64
	UsedBytes      uint64
	AvailableBytes uint64
	UsagePercent   float64
}

// CollectStorageMetrics gathers storage usage information for specified paths
func (m *MetricsCollector) CollectStorageMetrics(ctx context.Context, paths []string) []StorageInfo {
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	// Note: callers may request multiple paths, but higher-level services report
	// only the first volume to keep node metrics stable and comparable across
	// environments (host vs container overlays, multiple mount points, etc.).
	var storageInfos []StorageInfo
	for _, path := range paths {
		usage, err := disk.Usage(path)
		if err != nil {
			logtrace.Error(ctx, "failed to get storage info", logtrace.Fields{logtrace.FieldError: err.Error(), "path": path})
			continue
		}
		totalBytes := adjustDiskBytes(usage.Total)
		availableBytes := usage.Free
		availableBytes = min(availableBytes, totalBytes)
		usedBytes := totalBytes - availableBytes
		usagePercent := 0.0
		if totalBytes > 0 {
			usagePercent = float64(usedBytes) / float64(totalBytes) * 100
		}

		storageInfos = append(storageInfos, StorageInfo{
			Path:           path,
			TotalBytes:     totalBytes,
			UsedBytes:      usedBytes,
			AvailableBytes: availableBytes,
			UsagePercent:   usagePercent,
		})
	}
	return storageInfos
}
