package cascade

import (
	pb "github.com/LumeraProtocol/supernode/gen/supernode/supernode"
	"github.com/LumeraProtocol/supernode/supernode/node/common"
	"github.com/LumeraProtocol/supernode/supernode/services/cascade"
)

// this implements SN's GRPC methods that are called by another SNs during Cascade Registration
// meaning - these methods implements server side of SN to SN GRPC communication

// RegisterCascade represents grpc services for registration Sense tickets.
type RegisterCascade struct {
	pb.UnimplementedCascadeServiceServer

	*common.RegisterCascade
}

// NewRegisterCascade returns a new RegisterCascade instance.
func NewRegisterCascade(service *cascade.CascadeService) *RegisterCascade {
	return &RegisterCascade{
		RegisterCascade: common.NewRegisterCascade(service),
	}
}
