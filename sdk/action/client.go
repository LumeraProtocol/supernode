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

type Client interface {
	StartCascade(ctx context.Context, fileHash string, actionID string, filePath string, signedData string) (string, error)

	GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool)

	SubscribeToEvents(eventType event.EventType, handler event.Handler)

	SubscribeToAllEvents(handler event.Handler)
}

type ClientImpl struct {
	config      config.Config
	taskManager task.Manager
	logger      log.Logger
	keyring     keyring.Keyring
}

func NewClient(
	config config.Config,
	logger log.Logger,
	keyring keyring.Keyring,
) (Client, error) {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	taskManager, err := task.NewManager(config, logger, keyring)
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

func (ac *ClientImpl) StartCascade(
	ctx context.Context,
	fileHash string,
	actionID string,
	filePath string,
	signedData string,
) (string, error) {
	ac.logger.Debug(ctx, "Starting cascade operation",
		"fileHash", fileHash,
		"actionID", actionID,
		"filePath", filePath,
	)

	if fileHash == "" {
		ac.logger.Error(ctx, "Empty file hash provided")
		return "", ErrEmptyFileHash
	}
	if actionID == "" {
		ac.logger.Error(ctx, "Empty action ID provided")
		return "", ErrEmptyActionID
	}
	if filePath == "" {
		ac.logger.Error(ctx, "Empty file path provided")
		return "", ErrEmptyFilePath
	}

	taskID, err := ac.taskManager.CreateCascadeTask(ctx, fileHash, actionID, filePath, signedData)
	if err != nil {
		ac.logger.Error(ctx, "Failed to create cascade task", "error", err)
		return "", fmt.Errorf("failed to create cascade task: %w", err)
	}

	ac.logger.Info(ctx, "Cascade task created successfully", "taskID", taskID)
	return taskID, nil
}

func (ac *ClientImpl) GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool) {
	ac.logger.Debug(ctx, "Getting task", "taskID", taskID)
	task, found := ac.taskManager.GetTask(ctx, taskID)
	if !found {
		ac.logger.Debug(ctx, "Task not found", "taskID", taskID)
	} else {
		ac.logger.Debug(ctx, "Task found", "taskID", taskID, "status", task.Status)
	}
	return task, found
}

// SubscribeToEvents registers a handler for specific event types
func (ac *ClientImpl) SubscribeToEvents(eventType event.EventType, handler event.Handler) {
	ac.logger.Debug(context.Background(), "Subscribing to events via task manager", "eventType", eventType)
	if ac.taskManager != nil {
		ac.taskManager.SubscribeToEvents(eventType, handler)
	} else {
		ac.logger.Warn(context.Background(), "TaskManager is nil, cannot subscribe to events")
	}
}

// SubscribeToAllEvents registers a handler for all events
func (ac *ClientImpl) SubscribeToAllEvents(handler event.Handler) {
	ac.logger.Debug(context.Background(), "Subscribing to all events via task manager")
	if ac.taskManager != nil {
		ac.taskManager.SubscribeToAllEvents(handler)
	} else {
		ac.logger.Warn(context.Background(), "TaskManager is nil, cannot subscribe to all events")
	}
}
