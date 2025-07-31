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
	services      []ServiceInfo // Store service descriptors
}

// ServiceInfo holds information about a registered service
type ServiceInfo struct {
	Name    string
	Methods []string
}

// NewSupernodeServer creates a new SupernodeServer
func NewSupernodeServer(statusService *supernode.SupernodeStatusService) *SupernodeServer {
	return &SupernodeServer{
		statusService: statusService,
		services:      []ServiceInfo{},
	}
}

// RegisterService adds a service to the known services list
func (s *SupernodeServer) RegisterService(serviceName string, desc *grpc.ServiceDesc) {
	methods := make([]string, 0, len(desc.Methods)+len(desc.Streams))
	
	// Add unary methods
	for _, method := range desc.Methods {
		methods = append(methods, method.MethodName)
	}
	
	// Add streaming methods
	for _, stream := range desc.Streams {
		methods = append(methods, stream.StreamName)
	}
	
	s.services = append(s.services, ServiceInfo{
		Name:    serviceName,
		Methods: methods,
	})
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
		RunningTasks:       make([]*pb.StatusResponse_ServiceTasks, 0, len(status.RunningTasks)),
		RegisteredServices: status.RegisteredServices,
	}

	// Convert service tasks
	for _, service := range status.RunningTasks {
		serviceTask := &pb.StatusResponse_ServiceTasks{
			ServiceName: service.ServiceName,
			TaskIds:     service.TaskIDs,
			TaskCount:   service.TaskCount,
		}
		response.RunningTasks = append(response.RunningTasks, serviceTask)
	}

	return response, nil
}

// ListServices implements SupernodeService.ListServices
func (s *SupernodeServer) ListServices(ctx context.Context, req *pb.ListServicesRequest) (*pb.ListServicesResponse, error) {
	// Convert internal ServiceInfo to protobuf ServiceInfo
	services := make([]*pb.ServiceInfo, 0, len(s.services))
	for _, svc := range s.services {
		services = append(services, &pb.ServiceInfo{
			Name:    svc.Name,
			Methods: svc.Methods,
		})
	}

	return &pb.ListServicesResponse{
		Services: services,
		Count:    int32(len(services)),
	}, nil
}

// Desc implements the service interface for gRPC service registration
func (s *SupernodeServer) Desc() *grpc.ServiceDesc {
	return &pb.SupernodeService_ServiceDesc
}
