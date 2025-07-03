package common

import (
	"context"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

// StatusResponse represents system status
type StatusResponse struct {
	CPU struct {
		Usage     string
		Remaining string
	}
	Memory struct {
		Total     uint64
		Used      uint64
		Available uint64
		UsedPerc  float64
	}
	Services          []ServiceTasks
	AvailableServices []string
}

// ServiceTasks contains task information for a specific service
type ServiceTasks struct {
	ServiceName string
	TaskIDs     []string
	TaskCount   int32
}

// TaskProvider interface for services to provide their running tasks
type TaskProvider interface {
	GetServiceName() string
	GetRunningTasks() []string
}

// SupernodeStatusService provides centralized status information
type SupernodeStatusService struct {
	taskProviders []TaskProvider
}

// NewSupernodeStatusService creates a new supernode status service
func NewSupernodeStatusService() *SupernodeStatusService {
	return &SupernodeStatusService{
		taskProviders: make([]TaskProvider, 0),
	}
}

// RegisterTaskProvider registers a service as a task provider
func (s *SupernodeStatusService) RegisterTaskProvider(provider TaskProvider) {
	s.taskProviders = append(s.taskProviders, provider)
}

// GetStatus returns the current system status including all registered services
func (s *SupernodeStatusService) GetStatus(ctx context.Context) (StatusResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "GetStatus",
		logtrace.FieldModule: "SupernodeStatusService",
	}
	logtrace.Info(ctx, "status request received", fields)

	var resp StatusResponse

	// Get CPU information
	percentages, err := cpu.Percent(time.Second, false)
	if err != nil {
		logtrace.Error(ctx, "failed to get cpu info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return resp, err
	}

	usage := percentages[0]
	remaining := 100 - usage
	resp.CPU.Usage = fmt.Sprintf("%.2f", usage)
	resp.CPU.Remaining = fmt.Sprintf("%.2f", remaining)

	// Get Memory information
	vmem, err := mem.VirtualMemory()
	if err != nil {
		logtrace.Error(ctx, "failed to get memory info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return resp, err
	}
	resp.Memory.Total = vmem.Total
	resp.Memory.Used = vmem.Used
	resp.Memory.Available = vmem.Available
	resp.Memory.UsedPerc = vmem.UsedPercent

	// Get service information from all registered providers
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

	totalTasks := 0
	for _, service := range resp.Services {
		totalTasks += int(service.TaskCount)
	}

	logtrace.Info(ctx, "status data collected", logtrace.Fields{
		"cpu_usage":     fmt.Sprintf("%.2f", usage),
		"cpu_remaining": fmt.Sprintf("%.2f", remaining),
		"mem_total":     resp.Memory.Total,
		"mem_used":      resp.Memory.Used,
		"mem_used%":     resp.Memory.UsedPerc,
		"service_count": len(resp.Services),
		"total_tasks":   totalTasks,
	})

	return resp, nil
}
