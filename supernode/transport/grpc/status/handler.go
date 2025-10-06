package server

import (
	"context"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	pbcascade "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	statussvc "github.com/LumeraProtocol/supernode/v2/supernode/status"
)

// SupernodeServer implements the SupernodeService gRPC service
type SupernodeServer struct {
	pb.UnimplementedSupernodeServiceServer
	statusService *statussvc.SupernodeStatusService
}

// NewSupernodeServer creates a new SupernodeServer
func NewSupernodeServer(statusService *statussvc.SupernodeStatusService) *SupernodeServer {
	return &SupernodeServer{statusService: statusService}
}

// GetStatus implements SupernodeService.GetStatus
func (s *SupernodeServer) GetStatus(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	return s.statusService.GetStatus(ctx, req.GetIncludeP2PMetrics())
}

// ListServices implements SupernodeService.ListServices
func (s *SupernodeServer) ListServices(ctx context.Context, _ *pb.ListServicesRequest) (*pb.ListServicesResponse, error) {
	// Describe available services and methods/streams exposed by this node
	var services []*pb.ServiceInfo

	// SupernodeService methods
	var supernodeMethods []string
	for _, m := range pb.SupernodeService_ServiceDesc.Methods {
		supernodeMethods = append(supernodeMethods, m.MethodName)
	}
	services = append(services, &pb.ServiceInfo{
		Name:    pb.SupernodeService_ServiceDesc.ServiceName,
		Methods: supernodeMethods,
	})

	// CascadeService streams (surface stream names as methods for discovery)
	var cascadeMethods []string
	for _, st := range pbcascade.CascadeService_ServiceDesc.Streams {
		cascadeMethods = append(cascadeMethods, st.StreamName)
	}
	services = append(services, &pb.ServiceInfo{
		Name:    pbcascade.CascadeService_ServiceDesc.ServiceName,
		Methods: cascadeMethods,
	})

	return &pb.ListServicesResponse{Services: services, Count: int32(len(services))}, nil
}
