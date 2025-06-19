package action

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/sdk/config"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"
	"github.com/LumeraProtocol/supernode/sdk/task"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Client defines the interface for action operations
//
//go:generate mockery --name=Client --output=testutil/mocks --outpkg=mocks --filename=client_mock.go
type Client interface {
	//   - signature: Base64-encoded cryptographic signature of the file's data hash (blake3)
	//   	1- hash(blake3)  > 2- sign > 3- base64
	//     The signature must be created by the same account that created the Lumera action.
	//     It must be a digital signature of the data hash found in the action's CASCADE metadata.
	StartCascade(ctx context.Context, filePath string, actionID string, signature string) (string, error)
	DeleteTask(ctx context.Context, taskID string) error
	GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool)
	SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) error
	SubscribeToAllEvents(ctx context.Context, handler event.Handler) error
	DownloadCascade(ctx context.Context, actionID, outputPath string) (string, error)
}

// ClientImpl implements the Client interface
type ClientImpl struct {
	config      config.Config
	taskManager task.Manager
	logger      log.Logger
	keyring     keyring.Keyring
}

// Verify interface compliance at compile time
var _ Client = (*ClientImpl)(nil)

// NewClient creates a new action client
func NewClient(ctx context.Context, config config.Config, logger log.Logger) (Client, error) {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	taskManager, err := task.NewManager(ctx, config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	return &ClientImpl{
		config:      config,
		taskManager: taskManager,
		logger:      logger,
	}, nil
}

// StartCascade initiates a cascade operation
func (c *ClientImpl) StartCascade(ctx context.Context, filePath string, actionID string, signature string) (string, error) {
	if actionID == "" {
		c.logger.Error(ctx, "Empty action ID provided")
		return "", ErrEmptyActionID
	}
	if filePath == "" {
		c.logger.Error(ctx, "Empty file path provided")
		return "", ErrEmptyData
	}

	taskID, err := c.taskManager.CreateCascadeTask(ctx, filePath, actionID, signature)
	if err != nil {
		c.logger.Error(ctx, "Failed to create cascade task", "error", err)
		return "", fmt.Errorf("failed to create cascade task: %w", err)
	}

	c.logger.Info(ctx, "Cascade task created successfully", "taskID", taskID)
	return taskID, nil
}

// GetTask retrieves a task by its ID
func (c *ClientImpl) GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool) {
	task, found := c.taskManager.GetTask(ctx, taskID)
	if found {
		return task, true
	}
	c.logger.Debug(ctx, "Task not found", "taskID", taskID)

	return nil, false
}

// DeleteTask removes a task by its ID
func (c *ClientImpl) DeleteTask(ctx context.Context, taskID string) error {
	c.logger.Debug(ctx, "Deleting task", "taskID", taskID)
	if taskID == "" {
		c.logger.Error(ctx, "Empty task ID provided")
		return fmt.Errorf("task ID cannot be empty")
	}

	if err := c.taskManager.DeleteTask(ctx, taskID); err != nil {
		c.logger.Error(ctx, "Failed to delete task", "taskID", taskID, "error", err)
		return fmt.Errorf("failed to delete task: %w", err)
	}
	c.logger.Info(ctx, "Task deleted successfully", "taskID", taskID)

	return nil
}

// SubscribeToEvents registers a handler for specific event types
func (c *ClientImpl) SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) error {
	if c.taskManager == nil {
		return fmt.Errorf("TaskManager is nil, cannot subscribe to events")
	}

	c.logger.Debug(ctx, "Subscribing to events via task manager", "eventType", eventType)
	c.taskManager.SubscribeToEvents(ctx, eventType, handler)

	return nil
}

// SubscribeToAllEvents registers a handler for all events
func (c *ClientImpl) SubscribeToAllEvents(ctx context.Context, handler event.Handler) error {
	if c.taskManager == nil {
		return fmt.Errorf("TaskManager is nil, cannot subscribe to events")
	}

	c.logger.Debug(ctx, "Subscribing to all events via task manager")
	c.taskManager.SubscribeToAllEvents(ctx, handler)

	return nil
}

func (c *ClientImpl) DownloadCascade(
	ctx context.Context,
	actionID, outputPath string,
) (string, error) {

	if actionID == "" {
		return "", fmt.Errorf("actionID is empty")
	}

	taskID, err := c.taskManager.CreateDownloadTask(ctx, actionID, outputPath)
	if err != nil {
		return "", fmt.Errorf("create download task: %w", err)
	}

	c.logger.Info(ctx, "cascade download task created",
		"task_id", taskID,
		"action_id", actionID,
	)

	return taskID, nil
}
