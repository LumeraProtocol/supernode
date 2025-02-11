package collectionregister

import (
	"context"

	"github.com/LumeraProtocol/supernode/common/storage/queries"

	"github.com/LumeraProtocol/supernode/common/storage"
	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pastel"
	"github.com/LumeraProtocol/supernode/supernode/node"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

const (
	logPrefix = "collection"
)

// CollectionRegistrationService represent collection service.
type CollectionRegistrationService struct {
	*common.SuperNodeService
	config *Config

	nodeClient node.ClientInterface
	historyDB  queries.LocalStoreInterface
}

// Run starts task
func (service *CollectionRegistrationService) Run(ctx context.Context) error {
	return service.RunHelper(ctx, service.config.PastelID, logPrefix)
}

// NewCollectionRegistrationTask runs a new task of the registration Collection and returns its taskID.
func (service *CollectionRegistrationService) NewCollectionRegistrationTask() *CollectionRegistrationTask {
	task := NewCollectionRegistrationTask(service)
	service.Worker.AddTask(task)
	return task
}

// Task returns the task of the Collection registration by the given id.
func (service *CollectionRegistrationService) Task(id string) *CollectionRegistrationTask {
	return service.Worker.Task(id).(*CollectionRegistrationTask)
}

// NewService returns a new Service instance.
func NewService(config *Config,
	fileStorage storage.FileStorageInterface,
	pastelClient pastel.Client,
	nodeClient node.ClientInterface,
	p2pClient p2p.Client,
	historyDB queries.LocalStoreInterface,
) *CollectionRegistrationService {
	return &CollectionRegistrationService{
		SuperNodeService: common.NewSuperNodeService(fileStorage, pastelClient, p2pClient),
		config:           config,
		nodeClient:       nodeClient,
		historyDB:        historyDB,
	}
}
