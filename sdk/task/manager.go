package task

import (
	"context"
	"fmt"
	"path"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/config"
	"github.com/LumeraProtocol/supernode/sdk/event"
	taskstatus "github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/google/uuid"
)

const MAX_EVENT_WORKERS = 100

// Manager handles task creation and management
//
//go:generate mockery --name=Manager --output=testutil/mocks --outpkg=mocks --filename=manager_mock.go
type Manager interface {
	CreateCascadeTask(ctx context.Context, filePath string, actionID string, signature string) (string, error)
	GetTask(ctx context.Context, taskID string) (*TaskEntry, bool)
	DeleteTask(ctx context.Context, taskID string) error
	SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler)
	SubscribeToAllEvents(ctx context.Context, handler event.Handler)

	CreateDownloadTask(ctx context.Context, actionID, outputPath, signature string) (string, error)
}

type ManagerImpl struct {
	lumeraClient lumera.Client
	config       config.Config
	taskCache    *TaskCache
	eventBus     *event.Bus
	logger       log.Logger
	keyring      keyring.Keyring
}

func NewManager(ctx context.Context, config config.Config, logger log.Logger) (Manager, error) {
	// 1 - Logger
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	logger.Info(ctx, "Initializing task manager")

	// 3 - Create the Lumera client adapter
	clientAdapter, err := lumera.NewAdapter(ctx,
		lumera.ConfigParams{
			GRPCAddr: config.Lumera.GRPCAddr,
			ChainID:  config.Lumera.ChainID,
			KeyName:  config.Account.KeyName,
			Keyring:  config.Account.Keyring,
		},
		logger)

	if err != nil {
		panic(fmt.Sprintf("Failed to create Lumera client: %v", err))
	}

	return NewManagerWithLumeraClient(ctx, config, logger, clientAdapter)
}

func NewManagerWithLumeraClient(ctx context.Context, config config.Config, logger log.Logger, lumeraClient lumera.Client) (Manager, error) {
	// 1 - Logger
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	logger.Info(ctx, "Initializing task manager with provided lumera client")

	// 2 - Event bus
	eventBus := event.NewBus(ctx, logger, MAX_EVENT_WORKERS)

	taskCache, err := NewTaskCache(ctx, logger)
	if err != nil {
		logger.Error(ctx, "Failed to create task cache", "error", err)
		return nil, fmt.Errorf("failed to create task cache: %w", err)
	}

	return &ManagerImpl{
		lumeraClient: lumeraClient,
		config:       config,
		taskCache:    taskCache,
		eventBus:     eventBus,
		logger:       logger,
		keyring:      config.Account.Keyring,
	}, nil
}

// CreateCascadeTask creates and starts a Cascade task using the new pattern
func (m *ManagerImpl) CreateCascadeTask(ctx context.Context, filePath string, actionID, signature string) (string, error) {
	// First validate the action before creating the task
	action, err := m.validateAction(ctx, actionID)
	if err != nil {
		return "", err
	}

	// verify signature
	if err := m.validateSignature(ctx, action, signature); err != nil {
		return "", err
	}

	taskID := uuid.New().String()[:8]

	m.logger.Debug(ctx, "Generated task ID", "taskID", taskID)

	baseTask := BaseTask{
		TaskID:   taskID,
		ActionID: actionID,
		TaskType: TaskTypeCascade,
		Action:   action,
		client:   m.lumeraClient,
		keyring:  m.keyring,
		config:   m.config,
		onEvent:  m.handleEvent,
		logger:   m.logger,
	}

	// Create cascade-specific task
	task := NewCascadeTask(baseTask, filePath, actionID)

	// Store task in cache
	m.taskCache.Set(ctx, taskID, task, TaskTypeCascade, actionID)

	// Ensure task is stored before returning
	m.taskCache.Wait()

	go func() {
		m.logger.Debug(ctx, "Starting cascade task asynchronously", "taskID", taskID)
		err := task.Run(ctx)
		if err != nil {
			// Error handling is done via events in the task.Run method
			// This is just a failsafe in case something goes wrong
			m.logger.Error(ctx, "Cascade task failed with error", "taskID", taskID, "error", err)
		}
	}()

	m.logger.Info(ctx, "Cascade task created successfully", "taskID", taskID)
	return taskID, nil
}

// GetTask retrieves a task entry by its ID
func (m *ManagerImpl) GetTask(ctx context.Context, taskID string) (*TaskEntry, bool) {
	m.logger.Debug(ctx, "Getting task", "taskID", taskID)
	return m.taskCache.Get(ctx, taskID)
}

// DeleteTask removes a task from the cache by its ID
func (m *ManagerImpl) DeleteTask(ctx context.Context, taskID string) error {
	m.logger.Info(ctx, "Deleting task", "taskID", taskID)

	// First check if the task exists
	_, exists := m.taskCache.Get(ctx, taskID)
	if !exists {
		m.logger.Warn(ctx, "Task not found for deletion", "taskID", taskID)
		return fmt.Errorf("task not found: %s", taskID)
	}

	// Delete the task from the cache
	m.taskCache.Del(ctx, taskID)

	m.logger.Info(ctx, "Task deleted successfully", "taskID", taskID)
	return nil
}

// SubscribeToEvents registers a handler for specific event types
func (m *ManagerImpl) SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) {
	m.logger.Debug(ctx, "Subscribing to events", "eventType", eventType)
	if m.eventBus != nil {
		m.eventBus.Subscribe(ctx, eventType, handler)
	} else {
		m.logger.Warn(ctx, "EventBus is nil, cannot subscribe to events")
	}
}

// SubscribeToAllEvents registers a handler for all events
func (m *ManagerImpl) SubscribeToAllEvents(ctx context.Context, handler event.Handler) {
	m.logger.Debug(ctx, "Subscribing to all events")
	if m.eventBus != nil {
		m.eventBus.SubscribeAll(ctx, handler)
	} else {
		m.logger.Warn(ctx, "EventBus is nil, cannot subscribe to events")
	}
}

// handleEvent processes events from tasks and updates cache
func (m *ManagerImpl) handleEvent(ctx context.Context, e event.Event) {
	m.logger.Debug(ctx, "Handling task event", "taskID", e.TaskID, "taskType", e.TaskType, "eventType", e.Type)

	// Update the cache with the event
	m.taskCache.AddEvent(ctx, e.TaskID, e)

	// Update the task status based on event type
	switch e.Type {
	case event.SDKTaskStarted:
		m.logger.Info(ctx, "Task started", "taskID", e.TaskID, "taskType", e.TaskType)
		m.taskCache.UpdateStatus(ctx, e.TaskID, taskstatus.StatusActive, nil)
	case event.SDKTaskCompleted:
		m.logger.Info(ctx, "Task completed", "taskID", e.TaskID, "taskType", e.TaskType)
		m.taskCache.UpdateStatus(ctx, e.TaskID, taskstatus.StatusCompleted, nil)
	case event.SDKTaskFailed:
		var err error
		if errMsg, ok := e.Data[event.KeyError].(string); ok {
			err = fmt.Errorf("%s", errMsg)
			m.logger.Error(ctx, "Task failed", "taskID", e.TaskID, "taskType", e.TaskType, "error", errMsg)
		} else {
			m.logger.Error(ctx, "Task failed with unknown error", "taskID", e.TaskID, "taskType", e.TaskType)
		}
		m.taskCache.UpdateStatus(ctx, e.TaskID, taskstatus.StatusFailed, err)
	case event.SDKTaskTxHashReceived:
		// Capture and store transaction hash from event
		if txHash, ok := e.Data[event.KeyTxHash].(string); ok && txHash != "" {
			m.logger.Info(ctx, "Transaction hash received", "taskID", e.TaskID, "txHash", txHash)
			m.taskCache.UpdateTxHash(ctx, e.TaskID, txHash)
		}
	}

	// Forward to the global event bus if configured
	if m.eventBus != nil {
		m.logger.Debug(ctx, "Publishing event to event bus", "eventType", e.Type)
		m.eventBus.Publish(ctx, e)
	} else {
		m.logger.Debug(ctx, "No event bus configured, skipping event publishing")
	}
}

// Close cleans up resources when the manager is no longer needed
func (m *ManagerImpl) Close(ctx context.Context) {
	m.logger.Info(ctx, "Closing task manager")

	// Wait for any in-flight events to be processed
	if m.eventBus != nil {
		m.eventBus.WaitForHandlers(ctx)
	}

	if m.taskCache != nil {
		m.taskCache.Close(ctx)
	}
}

func (m *ManagerImpl) CreateDownloadTask(ctx context.Context, actionID string, outputDir string, signature string) (string, error) {
	// First validate the action before creating the task
	action, err := m.validateDownloadAction(ctx, actionID)
	if err != nil {
		return "", err
	}
	// Decode metadata to get the filename
	metadata, err := m.lumeraClient.DecodeCascadeMetadata(ctx, action)
	if err != nil {
		return "", fmt.Errorf("failed to decode cascade metadata: %w", err)
	}

	// Ensure we have a filename from metadata
	if metadata.FileName == "" {
		return "", fmt.Errorf("no filename found in cascade metadata")
	}

	// Ensure the output path includes the correct filename
	finalOutputPath := path.Join(outputDir, action.ID, metadata.FileName)

	taskID := uuid.New().String()[:8]

	m.logger.Debug(ctx, "Generated download task ID", "task_id", taskID, "final_output_path", finalOutputPath)

	baseTask := BaseTask{
		TaskID:   taskID,
		ActionID: actionID,
		TaskType: TaskTypeCascade,
		Action:   action,
		client:   m.lumeraClient,
		keyring:  m.keyring,
		config:   m.config,
		onEvent:  m.handleEvent,
		logger:   m.logger,
	}

	// Use the final output path with the correct filename
	task := NewCascadeDownloadTask(baseTask, actionID, finalOutputPath, signature)

	// Store task in cache
	m.taskCache.Set(ctx, taskID, task, TaskTypeCascade, actionID)

	// Ensure task is stored before returning
	m.taskCache.Wait()

	go func() {
		m.logger.Debug(ctx, "Starting download cascade task asynchronously", "taskID", taskID)
		err := task.Run(ctx)
		if err != nil {
			// Error handling is done via events in the task.Run method
			// This is just a failsafe in case something goes wrong
			m.logger.Error(ctx, "Download Cascade task failed with error", "taskID", taskID, "error", err)
		}
	}()

	m.logger.Info(ctx, "Download Cascade task created successfully", "taskID", taskID, "outputPath", finalOutputPath)
	return taskID, nil
}
