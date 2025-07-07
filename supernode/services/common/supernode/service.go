package supernode

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)


// SupernodeStatusService provides centralized status information
// by collecting system metrics and aggregating task information from registered services
type SupernodeStatusService struct {
	taskProviders []TaskProvider  // List of registered services that provide task information
	metrics       *MetricsCollector // System metrics collector for CPU and memory stats
}

// NewSupernodeStatusService creates a new supernode status service instance
func NewSupernodeStatusService() *SupernodeStatusService {
	return &SupernodeStatusService{
		taskProviders: make([]TaskProvider, 0),
		metrics:       NewMetricsCollector(),
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
	cpuUsage, cpuRemaining, err := s.metrics.CollectCPUMetrics(ctx)
	if err != nil {
		return resp, err
	}
	resp.CPU.Usage = cpuUsage
	resp.CPU.Remaining = cpuRemaining

	// Collect memory metrics
	memTotal, memUsed, memAvailable, memUsedPerc, err := s.metrics.CollectMemoryMetrics(ctx)
	if err != nil {
		return resp, err
	}
	resp.Memory.Total = memTotal
	resp.Memory.Used = memUsed
	resp.Memory.Available = memAvailable
	resp.Memory.UsedPerc = memUsedPerc

	// Collect service information from all registered providers
	resp.Services = make([]ServiceTasks, 0, len(s.taskProviders))
	resp.AvailableServices = make([]string, 0, len(s.taskProviders))

	for _, provider := range s.taskProviders {
		serviceName := provider.GetServiceName()
		tasks := provider.GetRunningTasks()

		serviceTask := ServiceTasks{
			ServiceName: serviceName,
			TaskIDs:     tasks,
			TaskCount:   int32(len(tasks)),
		}
		resp.Services = append(resp.Services, serviceTask)
		resp.AvailableServices = append(resp.AvailableServices, serviceName)
	}

	// Log summary statistics
	totalTasks := 0
	for _, service := range resp.Services {
		totalTasks += int(service.TaskCount)
	}

	logtrace.Info(ctx, "status data collected", logtrace.Fields{
		"cpu_usage":     cpuUsage,
		"cpu_remaining": cpuRemaining,
		"mem_total":     memTotal,
		"mem_used":      memUsed,
		"mem_used%":     memUsedPerc,
		"service_count": len(resp.Services),
		"total_tasks":   totalTasks,
	})

	return resp, nil
}
