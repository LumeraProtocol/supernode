package cmd

import (
	"context"
	"reflect"

	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
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
				logtrace.Error(ctx, "service stopped with an error", logtrace.Fields{"service": reflect.TypeOf(service).String(), "error": err})
			} else {
				logtrace.Info(ctx, "service stopped", logtrace.Fields{"service": reflect.TypeOf(service).String()})
			}
			return err
		})
	}

	return group.Wait()
}
