package task

import (
	"action/adapters/lumera"
	"action/config"
	"action/event"
	"action/log"
	"context"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

type TaskType string

const (
	TaskTypeSense   TaskType = "SENSE"
	TaskTypeCascade TaskType = "CASCADE"
)

// TaskStatus represents the possible states of a task
type TaskStatus string

const (
	StatusPending    TaskStatus = "PENDING"
	StatusProcessing TaskStatus = "PROCESSING"
	StatusCompleted  TaskStatus = "COMPLETED"
	StatusFailed     TaskStatus = "FAILED"
)

// EventCallback is a function that processes events from tasks
type EventCallback func(event.Event)

// Task is the interface that all task types must implement
type Task interface {
	Run(ctx context.Context) error
}

// BaseTask contains common fields and methods for all task types
type BaseTask struct {
	TaskID   string
	ActionID string
	TaskType TaskType
	Status   TaskStatus
	Err      error

	// Dependencies
	keyring keyring.Keyring
	client  lumera.Client
	config  config.Config
	onEvent EventCallback
	logger  log.Logger
}

// EmitEvent creates and sends an event with the specified type and data
func (t *BaseTask) EmitEvent(eventType event.EventType, data map[string]interface{}) {
	if t.onEvent != nil {
		e := event.NewEvent(eventType, t.TaskID, string(t.TaskType), data)
		t.onEvent(e)
	}
}
