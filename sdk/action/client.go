package action

import (
	"context"
	"fmt"

	"action/config"
	"action/event"
	"action/log"
	"action/task"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Client defines the interface for action operations
type Client interface {
	StartCascade(ctx context.Context, fileHash string, actionID string, filePath string, signedData string) (string, error)
	DeleteTask(ctx context.Context, taskID string) error
	GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool)
	SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler)
	SubscribeToAllEvents(ctx context.Context, handler event.Handler)
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
func NewClient(
	ctx context.Context,
	config config.Config,
	logger log.Logger,
	keyring keyring.Keyring,
) (Client, error) {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	taskManager, err := task.NewManager(ctx, config, logger, keyring)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	return &ClientImpl{
		config:      config,
		taskManager: taskManager,
		logger:      logger,
		keyring:     keyring,
	}, nil
}

// StartCascade initiates a cascade operation
func (c *ClientImpl) StartCascade(
	ctx context.Context,
	fileHash string, // Hash of the file to process
	actionID string, // ID of the action to perform
	filePath string, // Path to the file on disk
	signedData string, // Optional signed authorization data
) (string, error) {
	c.logger.Debug(ctx, "Starting cascade operation",
		"fileHash", fileHash,
		"actionID", actionID,
		"filePath", filePath,
	)

	if fileHash == "" {
		c.logger.Error(ctx, "Empty file hash provided")
		return "", ErrEmptyFileHash
	}
	if actionID == "" {
		c.logger.Error(ctx, "Empty action ID provided")
		return "", ErrEmptyActionID
	}
	if filePath == "" {
		c.logger.Error(ctx, "Empty file path provided")
		return "", ErrEmptyFilePath
	}

	taskID, err := c.taskManager.CreateCascadeTask(ctx, fileHash, actionID, filePath, signedData)
	if err != nil {
		c.logger.Error(ctx, "Failed to create cascade task", "error", err)
		return "", fmt.Errorf("failed to create cascade task: %w", err)
	}

	c.logger.Info(ctx, "Cascade task created successfully", "taskID", taskID)
	return taskID, nil
}

// GetTask retrieves a task by its ID
func (c *ClientImpl) GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool) {
	c.logger.Debug(ctx, "Getting task", "taskID", taskID)
	task, found := c.taskManager.GetTask(ctx, taskID)
	if !found {
		c.logger.Debug(ctx, "Task not found", "taskID", taskID)
	} else {
		c.logger.Debug(ctx, "Task found", "taskID", taskID, "status", task.Status)
	}
	return task, found
}

// DeleteTask removes a task by its ID
func (c *ClientImpl) DeleteTask(ctx context.Context, taskID string) error {
	c.logger.Debug(ctx, "Deleting task", "taskID", taskID)
	if taskID == "" {
		c.logger.Error(ctx, "Empty task ID provided")
		return fmt.Errorf("task ID cannot be empty")
	}

	err := c.taskManager.DeleteTask(ctx, taskID)
	if err != nil {
		c.logger.Error(ctx, "Failed to delete task", "taskID", taskID, "error", err)
		return fmt.Errorf("failed to delete task: %w", err)
	}

	c.logger.Info(ctx, "Task deleted successfully", "taskID", taskID)
	return nil
}

// SubscribeToEvents registers a handler for specific event types
func (c *ClientImpl) SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) {
	c.logger.Debug(ctx, "Subscribing to events via task manager", "eventType", eventType)
	if c.taskManager != nil {
		c.taskManager.SubscribeToEvents(ctx, eventType, handler)
	} else {
		c.logger.Warn(ctx, "TaskManager is nil, cannot subscribe to events")
	}
}

// SubscribeToAllEvents registers a handler for all events
func (c *ClientImpl) SubscribeToAllEvents(ctx context.Context, handler event.Handler) {
	c.logger.Debug(ctx, "Subscribing to all events via task manager")
	if c.taskManager != nil {
		c.taskManager.SubscribeToAllEvents(ctx, handler)
	} else {
		c.logger.Warn(ctx, "TaskManager is nil, cannot subscribe to all events")
	}
}
