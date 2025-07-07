package supernode

import (
	"context"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

// MetricsCollector handles system resource monitoring
type MetricsCollector struct{}

// NewMetricsCollector creates a new metrics collector instance
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

// CollectCPUMetrics gathers CPU usage information
// Returns usage and remaining percentages as formatted strings
func (m *MetricsCollector) CollectCPUMetrics(ctx context.Context) (usage, remaining string, err error) {
	percentages, err := cpu.Percent(time.Second, false)
	if err != nil {
		logtrace.Error(ctx, "failed to get cpu info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return "", "", err
	}

	usageFloat := percentages[0]
	remainingFloat := 100 - usageFloat
	
	return fmt.Sprintf("%.2f", usageFloat), fmt.Sprintf("%.2f", remainingFloat), nil
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