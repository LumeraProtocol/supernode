package errgroup

import (
	"context"
	"runtime/debug"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"

	"golang.org/x/sync/errgroup"
)

// A Group is a collection of goroutines working on subtasks that are part of the same overall task.
type Group struct {
	*errgroup.Group
}

// Go calls the given function in a new goroutine and tries to recover from panics.
func (group *Group) Go(fn func() error) {
	group.Group.Go(func() (err error) {
		defer errors.Recover(func(recErr error) {
			fields := logtrace.Fields{
				logtrace.FieldError:      recErr.Error(),
				logtrace.FieldStackTrace: debug.Stack(),
			}
			logtrace.Error(context.Background(), "errgroup panic", fields)
			err = recErr
		})
		return fn()
	})
}

// WithContext returns a new Group and an associated Context derived from ctx.
func WithContext(ctx context.Context) (*Group, context.Context) {
	group, ctx := errgroup.WithContext(ctx)
	return &Group{group}, ctx
}
