package dd

import (
	"context"
	"fmt"

	ddService "github.com/LumeraProtocol/dd-service"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type GetStatusRequest struct {
}

type GetStatusResponse struct {
	Version     string
	TaskCount   *ddService.TaskCount
	TaskMetrics *ddService.TaskMetrics
}

// GetStatus retrieves the status.
func (c *Client) GetStatus(ctx context.Context, req GetStatusRequest) (GetStatusResponse, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:  "GetStatus",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "getting status", fields)

	res, err := c.ddService.GetStatus(ctx, &ddService.GetStatusRequest{})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get status", fields)
		return GetStatusResponse{}, fmt.Errorf("dd get status error: %w", err)
	}

	logtrace.Info(ctx, "successfully got status", fields)
	return GetStatusResponse{
		Version:     res.GetVersion(),
		TaskCount:   res.GetTaskCount(),
		TaskMetrics: res.GetTaskMetrics(),
	}, nil
}
