package action

import (
	"context"
	"fmt"

	"action/config"
	"action/task"

	"github.com/LumeraProtocol/supernode/pkg/lumera"
)

// ActionClient is the main interface that exposes high-level operations
// for interacting with the Lumera Protocol ecosystem
type ActionClient interface {
	// StartSense initiates a Sense operation and returns a unique task ID
	StartSense(ctx context.Context, fileHash string, actionID string, filePath string) (string, error)

	// StartCascade initiates a Cascade operation and returns a unique task ID
	StartCascade(ctx context.Context, fileHash string, actionID string, filePath string) (string, error)
}

// ActionClientImpl implements the ActionClient interface
type ActionClientImpl struct {
	lumeraClient lumera.Client
	config       config.Config
	taskManager  task.Manager
}

// NewActionClient creates a new instance of ActionClientImpl
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
	// Input validation
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

// StartCascade initiates a Cascade operation
func (ac *ActionClientImpl) StartCascade(
	ctx context.Context,
	fileHash string,
	actionID string,
	filePath string,
) (string, error) {
	// Input validation
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
