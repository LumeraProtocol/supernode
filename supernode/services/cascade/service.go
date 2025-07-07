package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/supernode/services/cascade/adaptors"
	"github.com/LumeraProtocol/supernode/supernode/services/common/base"
	"github.com/LumeraProtocol/supernode/supernode/services/common/supernode"
)

type CascadeService struct {
	*base.SuperNodeService
	config *Config

	LumeraClient adaptors.LumeraClient
	P2P          adaptors.P2PService
	RQ           adaptors.CodecService
}

// Compile-time checks to ensure CascadeService implements required interfaces
var _ supernode.TaskProvider = (*CascadeService)(nil)
var _ CascadeServiceFactory = (*CascadeService)(nil)

// NewCascadeRegistrationTask creates a new task for cascade registration
func (service *CascadeService) NewCascadeRegistrationTask() CascadeTask {
	task := NewCascadeRegistrationTask(service)
	service.Worker.AddTask(task)
	return task
}

// Run starts the service
func (service *CascadeService) Run(ctx context.Context) error {
	return service.RunHelper(ctx, service.config.SupernodeAccountAddress, logPrefix)
}

// GetServiceName returns the name of the cascade service
func (service *CascadeService) GetServiceName() string {
	return "cascade"
}

// GetRunningTasks returns a list of currently running task IDs
func (service *CascadeService) GetRunningTasks() []string {
	var taskIDs []string
	for _, task := range service.Worker.Tasks() {
		taskIDs = append(taskIDs, task.ID())
	}
	return taskIDs
}

// NewCascadeService returns a new CascadeService instance
func NewCascadeService(config *Config, lumera lumera.Client, p2pClient p2p.Client, codec codec.Codec, rqstore rqstore.Store) *CascadeService {
	return &CascadeService{
		config:           config,
		SuperNodeService: base.NewSuperNodeService(p2pClient),
		LumeraClient:     adaptors.NewLumeraClient(lumera),
		P2P:              adaptors.NewP2PService(p2pClient, rqstore),
		RQ:               adaptors.NewCodecService(codec),
	}
}
