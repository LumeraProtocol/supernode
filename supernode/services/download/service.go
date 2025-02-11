package download

import (
	"context"

	"github.com/LumeraProtocol/supernode/common/storage/queries"

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pastel"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

const (
	logPrefix = "Nft-download"
)

// NftDownloaderService represent Nft service.
type NftDownloaderService struct {
	*common.SuperNodeService
	config    *Config
	historyDB queries.LocalStoreInterface
}

// Run starts task
func (service *NftDownloaderService) Run(ctx context.Context) error {
	return service.RunHelper(ctx, service.config.PastelID, logPrefix)
}

// NewNftDownloadingTask runs a new task of the downloading Nft and returns its taskID.
func (service *NftDownloaderService) NewNftDownloadingTask() *NftDownloadingTask {
	task := NewNftDownloadingTask(service)
	service.Worker.AddTask(task)
	return task
}

// Task returns the task of the registration Nft by the given id.
func (service *NftDownloaderService) Task(id string) *NftDownloadingTask {
	return service.Worker.Task(id).(*NftDownloadingTask)
}

// NewService returns a new Service instance.
func NewService(config *Config, pastelClient pastel.Client, p2pClient p2p.Client, historyDB queries.LocalStoreInterface) *NftDownloaderService {
	return &NftDownloaderService{
		SuperNodeService: common.NewSuperNodeService(nil, pastelClient, p2pClient),
		config:           config,
		historyDB:        historyDB,
	}
}
