package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

type CascadeService struct {
	*common.SuperNodeService
	config *Config

	lumeraClient  lumera.Client
	raptorQClient raptorq.ClientInterface
	rqstore       rqstore.Store
	raptorQ       raptorq.RaptorQ
}

// NewCascadeRegistrationTask creates a new task for cascade registration
func (s *CascadeService) NewCascadeRegistrationTask() *CascadeRegistrationTask {
	task := NewCascadeRegistrationTask(s)
	s.Worker.AddTask(task)
	return task
}

// Run starts the service
func (service *CascadeService) Run(ctx context.Context) error {
	return service.RunHelper(ctx, service.config.SupernodeAccountAddress, logPrefix)
}

// NewCascadeService returns a new CascadeService instance
func NewCascadeService(
	config *Config,
	lumera lumera.Client,
	p2pClient p2p.Client,
	rqC raptorq.RaptorQ,
	rqClient raptorq.ClientInterface,
	rqstore rqstore.Store,
) *CascadeService {
	return &CascadeService{
		config:           config,
		SuperNodeService: common.NewSuperNodeService(p2pClient),
		lumeraClient:     lumera,
		raptorQ:          rqC,
		raptorQClient:    rqClient,
		rqstore:          rqstore,
	}
}

// GetSNAddress returns the supernode account address
func (s *CascadeService) GetSNAddress() string {
	return s.config.SupernodeAccountAddress
}
