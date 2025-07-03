package server

import (
	"context"
	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

func (server *SupernodeActionServer) HealthCheck(ctx context.Context, _ *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	resp, err := server.factory.NewSupernodeTask().HealthCheck(ctx)
	if err != nil {
		logtrace.Error(ctx, "error retrieving health-check metrics for supernode", logtrace.Fields{})
		return nil, err
	}

	return &pb.HealthCheckResponse{
		Cpu: &pb.HealthCheckResponse_CPU{
			Usage:     resp.CPU.Usage,
			Remaining: resp.CPU.Remaining,
		},
		Memory: &pb.HealthCheckResponse_Memory{
			Total:     resp.Memory.Total,
			Used:      resp.Memory.Used,
			Available: resp.Memory.Available,
			UsedPerc:  resp.Memory.UsedPerc,
		},
		TasksInProgress: resp.TasksInProgress,
	}, nil
}
