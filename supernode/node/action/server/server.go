package server

import (
	cascadeGen "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	cascadeService "github.com/LumeraProtocol/supernode/supernode/service/cascade"
)

type CascadeActionServer struct {
	cascadeGen.UnimplementedCascadeServiceServer
	service *cascadeService.CascadeService
}
