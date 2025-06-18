//go:generate mockery --name=Task

package task

import (
	"context"
	"sync"

	"github.com/LumeraProtocol/supernode/pkg/common/task/state"
	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/random"
)

// Task represent a worker task.
type Task interface {
	state.State

	// ID returns id of the task.
	ID() string

	// Run starts the task.
	Run(ctx context.Context) error

	// Cancel tells a task to abandon its work.
	// Cancel may be called by multiple goroutines simultaneously.
	// After the first call, subsequent calls to a Cancel do nothing.
	Cancel()

	// Done returns a channel when the task is canceled.
	Done() <-chan struct{}

	// RunAction waits for new actions, starts handling each of them in a new goroutine.
	RunAction(ctx context.Context) error

	// NewAction creates a new action and passes for the execution.
	// It is used when it is necessary to run an action in the context of `Tasks` rather than the one who was called.
	NewAction(fn ActionFn) <-chan struct{}

	// CloseActionCh closes action ch
	CloseActionCh()
}

type task struct {
	state.State

	id string

	actionCh chan *Action

	doneMu    sync.Mutex
	doneCh    chan struct{}
	closeOnce sync.Once
}

// ID implements Task.ID
func (task *task) ID() string {
	return task.id
}

// Run implements Task.Run
func (task *task) Run(_ context.Context) error {
	return errors.New("task default run func not implemented")
}

// Cancel implements Task.Cancel
func (task *task) Cancel() {
	task.doneMu.Lock()
	defer task.doneMu.Unlock()

	select {
	case <-task.Done():
		logtrace.Debug(context.Background(), "task cancelled", logtrace.Fields{"task_id": task.ID()})
		return
	default:
		close(task.doneCh)
	}
}

// Done implements Task.Done
func (task *task) Done() <-chan struct{} {
	return task.doneCh
}

// RunAction implements Task.RunAction
func (task *task) RunAction(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	group, ctx := errgroup.WithContext(ctx)
	for {
		select {
		case <-ctx.Done():
			logtrace.Info(ctx, "context done", logtrace.Fields{"task_id": task.ID()})
		case <-task.Done():
			logtrace.Info(ctx, "task done", logtrace.Fields{"task_id": task.ID()})
			cancel()
		case action, ok := <-task.actionCh:
			if !ok {
				logtrace.Info(ctx, "action channel closed", logtrace.Fields{"task_id": task.ID()})
				return group.Wait()
			}

			currAction := action
			group.Go(func() error {
				defer close(currAction.doneCh)

				return currAction.fn(ctx)
			})
			continue
		}
		break
	}

	return group.Wait()
}

// CloseActionCh safely closes the action channel
func (task *task) CloseActionCh() {
	task.closeOnce.Do(func() {
		close(task.actionCh)
	})
}

// NewAction implements Task.NewAction
func (task *task) NewAction(fn ActionFn) <-chan struct{} {
	act := NewAction(fn)
	task.actionCh <- act
	return act.doneCh
}

// New returns a new task instance.
func New(status state.SubStatus) Task {
	taskID, _ := random.String(8, random.Base62Chars)

	return &task{
		State:    state.New(status, taskID),
		id:       taskID,
		doneCh:   make(chan struct{}),
		actionCh: make(chan *Action),
	}
}
