package task

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

var ErrAlreadyRunning = errors.New("task already running")

// Handle manages a running task with an optional watchdog.
// It ensures Start and End are paired, logs start/end, and auto-ends on timeout.
type Handle struct {
	tr      Tracker
	service string
	id      string
	stop    chan struct{}
	once    sync.Once
}

// Start starts tracking a task and returns a Handle that will ensure the
// task is ended. A watchdog is started to auto-end the task after timeout
// to avoid indefinitely stuck running tasks in status reporting.
func StartWith(tr Tracker, ctx context.Context, service, id string, timeout time.Duration) *Handle {
	if tr == nil || service == "" || id == "" {
		return &Handle{}
	}
	tr.Start(service, id)
	logtrace.Info(ctx, "task: started", logtrace.Fields{"service": service, "task_id": id})

	g := &Handle{tr: tr, service: service, id: id, stop: make(chan struct{})}
	if timeout > 0 {
		go func() {
			select {
			case <-time.After(timeout):
				g.endWith(ctx, true)
			case <-g.stop:
			}
		}()
	}
	return g
}

// StartUniqueWith starts tracking a task only if it's not already tracked for the same
// (service, id) pair. It returns ErrAlreadyRunning if the task is already in-flight.
func StartUniqueWith(tr Tracker, ctx context.Context, service, id string, timeout time.Duration) (*Handle, error) {
	if tr == nil || service == "" || id == "" {
		return &Handle{}, nil
	}

	if ts, ok := tr.(interface {
		TryStart(service, taskID string) bool
	}); ok {
		if !ts.TryStart(service, id) {
			return nil, ErrAlreadyRunning
		}
	} else { // fallback: can't enforce uniqueness with unknown Tracker implementations
		tr.Start(service, id)
	}

	logtrace.Info(ctx, "task: started", logtrace.Fields{"service": service, "task_id": id})
	g := &Handle{tr: tr, service: service, id: id, stop: make(chan struct{})}
	if timeout > 0 {
		go func() {
			select {
			case <-time.After(timeout):
				g.endWith(ctx, true)
			case <-g.stop:
			}
		}()
	}
	return g, nil
}

// End stops tracking the task. Safe to call multiple times.
func (g *Handle) End(ctx context.Context) {
	g.endWith(ctx, false)
}

// EndWith ends the guard and logs accordingly. If expired is true,
// it emits a warning and ends the task to avoid stuck status.
func (g *Handle) endWith(ctx context.Context, expired bool) {
	if g == nil || g.service == "" || g.id == "" {
		return
	}
	g.once.Do(func() {
		close(g.stop)
		if g.tr != nil {
			g.tr.End(g.service, g.id)
		}
		if expired {
			logtrace.Warn(ctx, "task: watchdog expired", logtrace.Fields{"service": g.service, "task_id": g.id})
		} else {
			logtrace.Info(ctx, "task: ended", logtrace.Fields{"service": g.service, "task_id": g.id})
		}
	})
}
