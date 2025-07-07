package server

import (
	"context"

	"google.golang.org/grpc"

	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/LumeraProtocol/supernode/supernode/services/common/supernode"
)

// SupernodeServer implements the SupernodeService gRPC service
type SupernodeServer struct {
	pb.UnimplementedSupernodeServiceServer
	statusService *supernode.SupernodeStatusService
}

// NewSupernodeServer creates a new SupernodeServer
func NewSupernodeServer(statusService *supernode.SupernodeStatusService) *SupernodeServer {
	return &SupernodeServer{
		statusService: statusService,
	}
}

// GetStatus implements SupernodeService.GetStatus
func (s *SupernodeServer) GetStatus(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	// Get status from the common service
	status, err := s.statusService.GetStatus(ctx)
	if err != nil {
		return nil, err
	}

	// Convert to protobuf response
	response := &pb.StatusResponse{
		Cpu: &pb.StatusResponse_CPU{
			Usage:     status.CPU.Usage,
			Remaining: status.CPU.Remaining,
		},
		Memory: &pb.StatusResponse_Memory{
			Total:     status.Memory.Total,
			Used:      status.Memory.Used,
			Available: status.Memory.Available,
			UsedPerc:  status.Memory.UsedPerc,
		},
		Services:          make([]*pb.StatusResponse_ServiceTasks, 0, len(status.Services)),
		AvailableServices: status.AvailableServices,
	}

	// Convert service tasks
	for _, service := range status.Services {
		serviceTask := &pb.StatusResponse_ServiceTasks{
			ServiceName: service.ServiceName,
			TaskIds:     service.TaskIDs,
			TaskCount:   service.TaskCount,
		}
		response.Services = append(response.Services, serviceTask)
	}

	return response, nil
}

// Desc implements the service interface for gRPC service registration
func (s *SupernodeServer) Desc() *grpc.ServiceDesc {
	return &pb.SupernodeService_ServiceDesc
}
