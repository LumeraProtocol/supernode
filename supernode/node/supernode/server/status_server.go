package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/LumeraProtocol/supernode/pkg/capabilities"
	"github.com/LumeraProtocol/supernode/supernode/services/common/supernode"
)

// SupernodeServer implements the SupernodeService gRPC service
type SupernodeServer struct {
	pb.UnimplementedSupernodeServiceServer
	statusService *supernode.SupernodeStatusService
	capabilities  *capabilities.Capabilities
}

// NewSupernodeServer creates a new SupernodeServer
func NewSupernodeServer(statusService *supernode.SupernodeStatusService, caps *capabilities.Capabilities) *SupernodeServer {
	return &SupernodeServer{
		statusService: statusService,
		capabilities:  caps,
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

// GetCapabilities implements SupernodeService.GetCapabilities
func (s *SupernodeServer) GetCapabilities(ctx context.Context, req *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
	if s.capabilities == nil {
		return nil, grpc.Errorf(codes.Internal, "capabilities not initialized")
	}

	// Convert action versions to protobuf format
	actionVersions := make(map[string]*pb.ActionVersions)
	for action, versions := range s.capabilities.ActionVersions {
		actionVersions[action] = &pb.ActionVersions{
			Versions: versions,
		}
	}

	// Create protobuf capabilities
	pbCapabilities := &pb.Capabilities{
		Version:          s.capabilities.Version,
		SupportedActions: s.capabilities.SupportedActions,
		ActionVersions:   actionVersions,
		Metadata:         s.capabilities.Metadata,
		Timestamp:        s.capabilities.Timestamp.Unix(),
	}

	return &pb.GetCapabilitiesResponse{
		Capabilities: pbCapabilities,
	}, nil
}

// Desc implements the service interface for gRPC service registration
func (s *SupernodeServer) Desc() *grpc.ServiceDesc {
	return &pb.SupernodeService_ServiceDesc
}
