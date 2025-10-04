package server

import (
    "context"

    pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
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
