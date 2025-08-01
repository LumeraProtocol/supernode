package supernode

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

// SupernodeStatusService provides centralized status information
// by collecting system metrics and aggregating task information from registered services
type SupernodeStatusService struct {
	taskProviders []TaskProvider    // List of registered services that provide task information
	metrics       *MetricsCollector // System metrics collector for CPU and memory stats
	storagePaths  []string          // Paths to monitor for storage metrics
}

// NewSupernodeStatusService creates a new supernode status service instance
func NewSupernodeStatusService() *SupernodeStatusService {
	return &SupernodeStatusService{
		taskProviders: make([]TaskProvider, 0),
		metrics:       NewMetricsCollector(),
		storagePaths:  []string{"/"}, // Default to monitoring root filesystem
	}
}

// RegisterTaskProvider registers a service as a task provider
// This allows the service to report its running tasks in status responses
func (s *SupernodeStatusService) RegisterTaskProvider(provider TaskProvider) {
	s.taskProviders = append(s.taskProviders, provider)
}

// GetStatus returns the current system status including all registered services
// This method collects CPU metrics, memory usage, and task information from all providers
func (s *SupernodeStatusService) GetStatus(ctx context.Context) (StatusResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "GetStatus",
		logtrace.FieldModule: "SupernodeStatusService",
	}
	logtrace.Info(ctx, "status request received", fields)

	var resp StatusResponse

	// Collect CPU metrics
	cpuUsage, err := s.metrics.CollectCPUMetrics(ctx)
	if err != nil {
		return resp, err
	}
	resp.Resources.CPU.UsagePercent = cpuUsage

	// Collect memory metrics
	memTotal, memUsed, memAvailable, memUsedPerc, err := s.metrics.CollectMemoryMetrics(ctx)
	if err != nil {
		return resp, err
	}
	resp.Resources.Memory.TotalBytes = memTotal
	resp.Resources.Memory.UsedBytes = memUsed
	resp.Resources.Memory.AvailableBytes = memAvailable
	resp.Resources.Memory.UsagePercent = memUsedPerc

	// Collect storage metrics
	resp.Resources.Storage = s.metrics.CollectStorageMetrics(ctx, s.storagePaths)

	// Collect service information from all registered providers
	resp.RunningTasks = make([]ServiceTasks, 0, len(s.taskProviders))
	resp.RegisteredServices = make([]string, 0, len(s.taskProviders))

	for _, provider := range s.taskProviders {
		serviceName := provider.GetServiceName()
		tasks := provider.GetRunningTasks()

		// Add to registered services list
		resp.RegisteredServices = append(resp.RegisteredServices, serviceName)

		// Add all services to running tasks (even with 0 tasks)
		serviceTask := ServiceTasks{
			ServiceName: serviceName,
			TaskIDs:     tasks,
			TaskCount:   int32(len(tasks)),
		}
		resp.RunningTasks = append(resp.RunningTasks, serviceTask)
	}

	// Log summary statistics
	totalTasks := 0
	for _, service := range resp.RunningTasks {
		totalTasks += int(service.TaskCount)
	}

	logtrace.Info(ctx, "status data collected", logtrace.Fields{
		"cpu_usage%":      cpuUsage,
		"mem_total":       memTotal,
		"mem_used":        memUsed,
		"mem_usage%":      memUsedPerc,
		"storage_volumes": len(resp.Resources.Storage),
		"service_count":   len(resp.RunningTasks),
		"total_tasks":     totalTasks,
	})

	return resp, nil
}
