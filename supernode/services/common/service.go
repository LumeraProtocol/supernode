package common

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/common/task"
	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
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
	// *files.Storage

	P2PClient p2p.Client
}

// run starts task
func (service *SuperNodeService) run(ctx context.Context, nodeID string, prefix string) error {
	ctx = log.ContextWithPrefix(ctx, prefix)

	if nodeID == "" {
		return errors.New("PastelID is not specified in the config file")
	}

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return service.Worker.Run(ctx)
	})

	return group.Wait()
}

// RunHelper common code for Service runner
func (service *SuperNodeService) RunHelper(ctx context.Context, nodeID string, prefix string) error {
	for {
		select {
		case <-ctx.Done():
			log.WithContext(ctx).Error("context done - closing sn services")
			return nil
		case <-time.After(5 * time.Second):
			if err := service.run(ctx, nodeID, prefix); err != nil {
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
	p2pClient p2p.Client,
) *SuperNodeService {
	return &SuperNodeService{
		Worker:    task.NewWorker(),
		P2PClient: p2pClient,
	}
}
