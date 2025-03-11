package common

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/common/task"
	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/storage"
	"github.com/LumeraProtocol/supernode/pkg/storage/files"
)

// SuperNodeServiceInterface common interface for Services
type SuperNodeServiceInterface interface {
	RunHelper(ctx context.Context) error
	NewTask() task.Task
	Task(id string) task.Task
}

// SuperNodeService common "class" for Services
type SuperNodeService struct {
	*task.Worker
	*files.Storage

	P2PClient p2p.Client
}

// run starts task
func (service *SuperNodeService) run(ctx context.Context, pastelID string, prefix string) error {
	ctx = log.ContextWithPrefix(ctx, prefix)

	if pastelID == "" {
		return errors.New("PastelID is not specified in the config file")
	}

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return service.Worker.Run(ctx)
	})
	if service.Storage != nil {
		group.Go(func() error {
			return service.Storage.Run(ctx)
		})
	}
	return group.Wait()
}

// RunHelper common code for Service runner
func (service *SuperNodeService) RunHelper(ctx context.Context, pastelID string, prefix string) error {
	for {
		select {
		case <-ctx.Done():
			log.WithContext(ctx).Error("context done - closing sn services")
			return nil
		case <-time.After(5 * time.Second):
			if err := service.run(ctx, pastelID, prefix); err != nil {
				service.Worker = task.NewWorker()
				log.WithContext(ctx).WithError(err).Error("Service run failed, retrying")
			} else {
				log.WithContext(ctx).Info("Service run completed successfully - closing sn services")
				return nil
			}
		}
	}
}

// NewSuperNodeService creates SuperNodeService
func NewSuperNodeService(
	fileStorage storage.FileStorageInterface,
	p2pClient p2p.Client,
) *SuperNodeService {
	return &SuperNodeService{
		Worker:    task.NewWorker(),
		Storage:   files.NewStorage(fileStorage),
		P2PClient: p2pClient,
	}
}
