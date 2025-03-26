package task

import (
	"context"
)

// TaskStatus represents the possible states of a task
type TaskStatus string

const (
	StatusPending    TaskStatus = "PENDING"
	StatusProcessing TaskStatus = "PROCESSING"
	StatusCompleted  TaskStatus = "COMPLETED"
	StatusFailed     TaskStatus = "FAILED"
)

// Task is the interface that all task types must implement
type Task interface {
	// Run executes the task asynchronously
	Run(ctx context.Context) error

	// GetTaskID returns the unique identifier for this task
	GetTaskID() string

	// GetStatus returns the current status of the task
	GetStatus() TaskStatus

	// GetError returns the error if the task failed
	GetError() error
}

// BaseTask contains common fields and functionality for all tasks
type BaseTask struct {
	TaskID string
	Status TaskStatus
	Err    error
}

// GetTaskID returns the unique identifier for this task
func (t *BaseTask) GetTaskID() string {
	return t.TaskID
}

// GetStatus returns the current status of the task
func (t *BaseTask) GetStatus() TaskStatus {
	return t.Status
}

// GetError returns the error if the task failed
func (t *BaseTask) GetError() error {
	return t.Err
}
