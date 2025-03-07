package cascade

import (
	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/lumera/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/supernode"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/storage"
	"github.com/LumeraProtocol/supernode/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/supernode/node"
	"github.com/LumeraProtocol/supernode/supernode/service/common"
)

type CascadeService struct {
	*common.SuperNodeService
	config *Config

	supernodeAddress string

	lumeraClient    *lumera.Client
	supernodeClient *supernode.Client
	actionClient    *action.Client
	rqClient        *raptorq.Client

	nodeClient node.ClientInterface
	rqstore    rqstore.Store
	historyDB  queries.LocalStoreInterface
}

// NewCascadeRegistrationTask runs a new task of the registration Sense and returns its taskID.
func (service *CascadeService) NewCascadeRegistrationTask() *CascadeRegistrationTask {
	task := NewCascadeRegistrationTask(service)
	service.Worker.AddTask(task)

	return task
}

// Task returns the task of the Sense registration by the given id.
func (service *CascadeService) Task(id string) *CascadeRegistrationTask {
	if service.Worker.Task(id) == nil {
		return nil
	}

	return service.Worker.Task(id).(*CascadeRegistrationTask)
}

// NewCascadeService returns a new CascadeService instance.
func NewCascadeService(config *Config, supernodeAddress string, lumeraC *lumera.Client, actionC *action.Client,
	fileStorage storage.FileStorageInterface, nodeClient node.ClientInterface, p2pClient p2p.Client,
	supernodeC *supernode.Client, rqC *raptorq.Client, historyDB queries.LocalStoreInterface, rqstore rqstore.Store) *CascadeService {
	return &CascadeService{
		config:           config,
		SuperNodeService: common.NewSuperNodeService(fileStorage, p2pClient),
		supernodeAddress: supernodeAddress,
		lumeraClient:     lumeraC,
		actionClient:     actionC,
		supernodeClient:  supernodeC,
		nodeClient:       nodeClient,
		rqClient:         rqC,
		historyDB:        historyDB,
		rqstore:          rqstore,
	}
}

func (s *CascadeService) GetSNAddress() string {
	return s.supernodeAddress
}
