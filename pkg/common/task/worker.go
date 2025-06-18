package task

import (
	"context"
	"sync"

	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

// Worker represents a pool of the task.
type Worker struct {
	sync.Mutex

	tasks  []Task
	taskCh chan Task
}

// Tasks returns all tasks.
func (worker *Worker) Tasks() []Task {
	return worker.tasks
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
	return &Worker{
		taskCh: make(chan Task),
	}
}
