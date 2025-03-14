package cascade

import (
	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/storage"
	"github.com/LumeraProtocol/supernode/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	node "github.com/LumeraProtocol/supernode/supernode/node/supernode"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

type CascadeService struct {
	*common.SuperNodeService
	config *Config

	lumeraClient  lumera.Client
	raptorQ       raptorq.RaptorQ
	raptorQClient raptorq.ClientInterface

	nodeClient node.ClientInterface
	rqstore    rqstore.Store
	historyDB  queries.LocalStoreInterface
}

// NewCascadeRegistrationTask runs a new task of the registration Sense and returns its taskID.
func (s *CascadeService) NewCascadeRegistrationTask() *CascadeRegistrationTask {
	task := NewCascadeRegistrationTask(s)
	s.Worker.AddTask(task)

	return task
}

// Task returns the task of the Sense registration by the given id.
func (s *CascadeService) Task(id string) *CascadeRegistrationTask {
	if s.Worker.Task(id) == nil {
		return nil
	}

	return s.Worker.Task(id).(*CascadeRegistrationTask)
}

// NewCascadeService returns a new CascadeService instance.
func NewCascadeService(config *Config,
	lumera lumera.Client,
	fileStorage storage.FileStorageInterface,
	nodeClient node.ClientInterface,
	p2pClient p2p.Client,
	rqC raptorq.RaptorQ,
	rqClient raptorq.ClientInterface,
	rqstore rqstore.Store,
) *CascadeService {
	return &CascadeService{
		config:           config,
		SuperNodeService: common.NewSuperNodeService(fileStorage, p2pClient),
		lumeraClient:     lumera,
		nodeClient:       nodeClient,
		raptorQ:          rqC,
		raptorQClient:    rqClient,
		rqstore:          rqstore,
	}
}

func (s *CascadeService) GetSNAddress() string {
	return s.config.SupernodeAccountAddress // FIXME : verify
}
