package cascade

import (
	"context"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type HealthCheckResponse struct {
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
	TasksInProgress []string
}

func (task *CascadeRegistrationTask) HealthCheck(ctx context.Context) (HealthCheckResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "HealthCheck",
		logtrace.FieldModule: "CascadeActionServer",
	}
	logtrace.Info(ctx, "healthcheck request received", fields)

	var resp HealthCheckResponse

	percentages, err := cpu.Percent(time.Second, false)
	if err != nil {
		logtrace.Error(ctx, "failed to get cpu info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return resp, err
	}
	fmt.Println(percentages)
	usage := percentages[0]
	remaining := 100 - usage

	// Set CPU values in response
	resp.CPU.Usage = fmt.Sprintf("%.2f", usage)
	resp.CPU.Remaining = fmt.Sprintf("%.2f", remaining)

	// Memory stats
	vmem, err := mem.VirtualMemory()
	if err != nil {
		logtrace.Error(ctx, "failed to get memory info", logtrace.Fields{logtrace.FieldError: err.Error()})
		return resp, err
	}
	resp.Memory.Total = vmem.Total
	resp.Memory.Used = vmem.Used
	resp.Memory.Available = vmem.Available
	resp.Memory.UsedPerc = vmem.UsedPercent

	// Tasks
	for _, t := range task.Worker.Tasks() {
		resp.TasksInProgress = append(resp.TasksInProgress, t.ID())
	}

	logtrace.Info(ctx, "top-style healthcheck data", logtrace.Fields{"cpu_usage": fmt.Sprintf("%.2f", usage), "cpu_remaining": fmt.Sprintf("%.2f", remaining), "mem_total": resp.Memory.Total, "mem_used": resp.Memory.Used, "mem_used%": resp.Memory.UsedPerc, "task_count": len(resp.TasksInProgress)})

	return resp, nil
}
