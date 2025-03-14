package cascade

import (
	cascadeGen "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/supernode/node/common"
	"github.com/LumeraProtocol/supernode/supernode/services/cascade"
)

type CascadeActionServer struct {
	cascadeGen.UnimplementedCascadeServiceServer

	*common.RegisterCascade
}

// NewCascadeActionServer returns a new CascadeActionServer instance.
func NewCascadeActionServer(service *cascade.CascadeService) *CascadeActionServer {
	return &CascadeActionServer{
		RegisterCascade: common.NewRegisterCascade(service),
	}
}
