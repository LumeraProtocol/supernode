package server

import (
	"google.golang.org/grpc"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/supernode"
	"github.com/LumeraProtocol/supernode/supernode/node/common"
	"github.com/LumeraProtocol/supernode/supernode/service/cascade"
)

// this implements SN's GRPC methods that are called by another SNs during Cascade Registration
// meaning - these methods implements server side of SN to SN GRPC communication

// RegisterCascade represents grpc service for registration Sense tickets.
type RegisterCascade struct {
	pb.UnimplementedCascadeServiceServer

	*common.RegisterCascade
}

// Desc returns a description of the service.
func (service *RegisterCascade) Desc() *grpc.ServiceDesc {
	return &pb.CascadeService_ServiceDesc
}

// NewRegisterCascade returns a new RegisterCascade instance.
func NewRegisterCascade(service *cascade.CascadeService) *RegisterCascade {
	return &RegisterCascade{
		RegisterCascade: common.NewRegisterCascade(service),
	}
}
