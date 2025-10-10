package task

import (
	"context"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// Handle manages a running task with an optional watchdog.
// It ensures Start and End are paired, logs start/end, and auto-ends on timeout.
type Handle struct {
	service string
	id      string
	stop    chan struct{}
	once    sync.Once
}

// Start starts tracking a task and returns a Handle that will ensure the
// task is ended. A watchdog is started to auto-end the task after timeout
// to avoid indefinitely stuck running tasks in status reporting.
func Start(ctx context.Context, service, id string, timeout time.Duration) *Handle {
	if service == "" || id == "" {
		return &Handle{}
	}
	Default.Start(service, id)
	logtrace.Info(ctx, "task: started", logtrace.Fields{"service": service, "task_id": id})

	g := &Handle{service: service, id: id, stop: make(chan struct{})}
	if timeout > 0 {
		go func() {
			select {
			case <-time.After(timeout):
				// Auto-end if not already ended
				g.endWith(ctx, true)
			case <-g.stop:
				// normal completion
			}
		}()
	}
	return g
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
		Default.End(g.service, g.id)
		if expired {
			logtrace.Warn(ctx, "task: watchdog expired", logtrace.Fields{"service": g.service, "task_id": g.id})
		} else {
			logtrace.Info(ctx, "task: ended", logtrace.Fields{"service": g.service, "task_id": g.id})
		}
	})
}
