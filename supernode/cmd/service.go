package cmd

import (
	"context"
	"reflect"

	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/log"
)

type service interface {
	Run(context.Context) error
}

func RunServices(ctx context.Context, services ...service) error {
	group, ctx := errgroup.WithContext(ctx)

	for _, service := range services {
		service := service

		group.Go(func() error {
			err := service.Run(ctx)
			if err != nil {
				log.WithContext(ctx).WithError(err).Errorf("service %s stopped", reflect.TypeOf(service))
			} else {
				log.WithContext(ctx).Warnf("service %s stopped", reflect.TypeOf(service))
			}
			return err
		})
	}

	return group.Wait()
}
