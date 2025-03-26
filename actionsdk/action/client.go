package action

import (
	"context"
	"fmt"

	"action/config"
	"action/event"
	"action/task"

	"github.com/LumeraProtocol/supernode/pkg/lumera"
)

type ActionClient interface {
	// StartSense initiates a Sense operation and returns a unique task ID
	StartSense(ctx context.Context, fileHash string, actionID string, filePath string) (string, error)

	// StartCascade initiates a Cascade operation and returns a unique task ID
	StartCascade(ctx context.Context, fileHash string, actionID string, filePath string) (string, error)

	// SubscribeToEvents registers a handler for specific event types
	SubscribeToEvents(eventType event.EventType, handler event.Handler)

	// SubscribeToAllEvents registers a handler for all events
	SubscribeToAllEvents(handler event.Handler)
}

type ActionClientImpl struct {
	lumeraClient lumera.Client
	config       config.Config
	taskManager  task.Manager
}

func NewActionClient(lumeraClient lumera.Client, config config.Config) *ActionClientImpl {
	// Create task manager with config
	taskManager := task.NewManager(lumeraClient, config)

	return &ActionClientImpl{
		lumeraClient: lumeraClient,
		config:       config,
		taskManager:  taskManager,
	}
}

// StartSense initiates a Sense operation
func (ac *ActionClientImpl) StartSense(
	ctx context.Context,
	fileHash string,
	actionID string,
	filePath string,
) (string, error) {

	if fileHash == "" {
		return "", ErrEmptyFileHash
	}
	if actionID == "" {
		return "", ErrEmptyActionID
	}
	if filePath == "" {
		return "", ErrEmptyFilePath
	}

	// Create and start the task
	taskID, err := ac.taskManager.CreateSenseTask(ctx, fileHash, actionID, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create sense task: %w", err)
	}

	return taskID, nil
}

func (ac *ActionClientImpl) StartCascade(
	ctx context.Context,
	fileHash string,
	actionID string,
	filePath string,
) (string, error) {

	if fileHash == "" {
		return "", ErrEmptyFileHash
	}
	if actionID == "" {
		return "", ErrEmptyActionID
	}
	if filePath == "" {
		return "", ErrEmptyFilePath
	}

	// Create and start the task
	taskID, err := ac.taskManager.CreateCascadeTask(ctx, fileHash, actionID, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create cascade task: %w", err)
	}

	return taskID, nil
}

// SubscribeToEvents registers a handler for specific event types
func (ac *ActionClientImpl) SubscribeToEvents(eventType event.EventType, handler event.Handler) {
	if ac.config.EventBus != nil {
		ac.config.EventBus.Subscribe(eventType, handler)
	}
}

// SubscribeToAllEvents registers a handler for all events
func (ac *ActionClientImpl) SubscribeToAllEvents(handler event.Handler) {
	if ac.config.EventBus != nil {
		ac.config.EventBus.SubscribeAll(handler)
	}
}
