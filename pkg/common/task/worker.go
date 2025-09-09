package task

import (
	"context"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// Worker represents a pool of the task.
type Worker struct {
	sync.Mutex

	tasks  []Task
	taskCh chan Task
}

// Tasks returns all tasks.
func (worker *Worker) Tasks() []Task {
	worker.Lock()
	defer worker.Unlock()

	// return a shallow copy to avoid data races
	copied := make([]Task, len(worker.tasks))
	copy(copied, worker.tasks)
	return copied
}

// Task returns the task  by the given id.
func (worker *Worker) Task(taskID string) Task {
	worker.Lock()
	defer worker.Unlock()

	for _, task := range worker.tasks {
		if task.ID() == taskID {
			return task
		}
	}
	return nil
}

// AddTask adds the new task.
func (worker *Worker) AddTask(task Task) {
	worker.Lock()
	defer worker.Unlock()

	worker.tasks = append(worker.tasks, task)
	worker.taskCh <- task

	// Proactively remove the task once it's done to prevent lingering entries
	go func(t Task) {
		<-t.Done()
		// remove promptly when the task signals completion/cancelation
		worker.RemoveTask(t)
	}(task)
}

// RemoveTask removes the task.
func (worker *Worker) RemoveTask(subTask Task) {
	worker.Lock()
	defer worker.Unlock()

	for i, task := range worker.tasks {
		if task == subTask {
			worker.tasks = append(worker.tasks[:i], worker.tasks[i+1:]...)
			return
		}
	}
}

// Run waits for new tasks, starts handling each of them in a new goroutine.
func (worker *Worker) Run(ctx context.Context) error {
	group, _ := errgroup.WithContext(ctx) // Create an error group but ignore the derived context
	// Background sweeper to prune finalized tasks that might linger
	// even if the task's Run wasn't executed to completion.
	sweeperCtx, sweeperCancel := context.WithCancel(ctx)
	defer sweeperCancel()
	go worker.cleanupLoop(sweeperCtx)
	for {
		select {
		case <-ctx.Done():
			logtrace.Warn(ctx, "Worker run stopping", logtrace.Fields{logtrace.FieldError: ctx.Err().Error()})
			return group.Wait()
		case t := <-worker.taskCh: // Rename here
			currentTask := t // Capture the loop variable
			group.Go(func() error {
				defer func() {
					if r := recover(); r != nil {
						logtrace.Error(ctx, "Recovered from panic in common task's worker run", logtrace.Fields{"task": currentTask.ID(), "error": r})
					}

					logtrace.Info(ctx, "Task Removed", logtrace.Fields{"task": currentTask.ID()})
					// Remove the task from the worker's task list
					worker.RemoveTask(currentTask)
				}()

				return currentTask.Run(ctx) // Use the captured variable
			})
		}
	}
}

// NewWorker returns a new Worker instance.
func NewWorker() *Worker {
	w := &Worker{taskCh: make(chan Task)}
	return w
}

// cleanupLoop periodically removes tasks that are in a final state for a grace period
func (worker *Worker) cleanupLoop(ctx context.Context) {
	const (
		cleanupInterval = 30 * time.Second
		finalTaskTTL    = 2 * time.Minute
	)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			worker.Lock()
			// iterate and compact in-place
			kept := worker.tasks[:0]
			for _, t := range worker.tasks {
				st := t.Status()
				if st != nil && st.SubStatus != nil && st.SubStatus.IsFinal() {
					if now.Sub(st.CreatedAt) >= finalTaskTTL {
						// drop this finalized task
						continue
					}
				}
				kept = append(kept, t)
			}
			worker.tasks = kept
			worker.Unlock()
		}
	}
}
