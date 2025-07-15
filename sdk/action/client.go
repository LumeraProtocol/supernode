package action

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/sdk/config"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"
	"github.com/LumeraProtocol/supernode/sdk/net"
	"github.com/LumeraProtocol/supernode/sdk/task"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Client defines the interface for action operations
//
//go:generate mockery --name=Client --output=testutil/mocks --outpkg=mocks --filename=client_mock.go
type Client interface {
	// StartCascade initiates a cascade operation with file path, action ID, and signature
	// signature: Base64-encoded signature of file's blake3 hash by action creator
	StartCascade(ctx context.Context, filePath string, actionID string, signature string) (string, error)
	DeleteTask(ctx context.Context, taskID string) error
	GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool)
	SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) error
	SubscribeToAllEvents(ctx context.Context, handler event.Handler) error
	GetSupernodeStatus(ctx context.Context, supernodeAddress string) (*supernodeservice.SupernodeStatusresponse, error)
	// DownloadCascade downloads cascade to outputDir, filename determined by action ID
	DownloadCascade(ctx context.Context, actionID, outputDir, signature string) (string, error)
}

// ClientImpl implements the Client interface
type ClientImpl struct {
	config       config.Config
	taskManager  task.Manager
	logger       log.Logger
	keyring      keyring.Keyring
	lumeraClient lumera.Client
}

// Verify interface compliance at compile time
var _ Client = (*ClientImpl)(nil)

// NewClient creates a new action client
func NewClient(ctx context.Context, config config.Config, logger log.Logger) (Client, error) {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	// Create lumera client once
	lumeraClient, err := lumera.NewAdapter(ctx,
		lumera.ConfigParams{
			GRPCAddr: config.Lumera.GRPCAddr,
			ChainID:  config.Lumera.ChainID,
			KeyName:  config.Account.KeyName,
			Keyring:  config.Account.Keyring,
		},
		logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create lumera client: %w", err)
	}

	// Create task manager with shared lumera client
	taskManager, err := task.NewManagerWithLumeraClient(ctx, config, logger, lumeraClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	return &ClientImpl{
		config:       config,
		taskManager:  taskManager,
		logger:       logger,
		keyring:      config.Account.Keyring,
		lumeraClient: lumeraClient,
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

// GetSupernodeStatus retrieves the status of a specific supernode by its address
func (c *ClientImpl) GetSupernodeStatus(ctx context.Context, supernodeAddress string) (*supernodeservice.SupernodeStatusresponse, error) {
	if supernodeAddress == "" {
		c.logger.Error(ctx, "Empty supernode address provided")
		return nil, fmt.Errorf("supernode address cannot be empty")
	}

	c.logger.Debug(ctx, "Getting supernode status", "address", supernodeAddress)

	// Get supernode details from blockchain
	supernode, err := c.lumeraClient.GetSupernodeBySupernodeAddress(ctx, supernodeAddress)
	if err != nil {
		c.logger.Error(ctx, "Failed to get supernode details", "address", supernodeAddress, "error", err)
		return nil, fmt.Errorf("failed to get supernode details: %w", err)
	}

	// Get the latest IP address for the supernode
	if len(supernode.PrevIpAddresses) == 0 {
		return nil, fmt.Errorf("no IP addresses found for supernode %s", supernodeAddress)
	}

	ipAddress := supernode.PrevIpAddresses[0].Address

	// Create lumera supernode object for network client
	lumeraSupernode := lumera.Supernode{
		CosmosAddress: supernodeAddress,
		GrpcEndpoint:  ipAddress,
		State:         lumera.SUPERNODE_STATE_ACTIVE, // Assume active since we're querying
	}

	// Create network client factory
	clientFactory := net.NewClientFactory(ctx, c.logger, c.keyring, c.lumeraClient, net.FactoryConfig{
		LocalCosmosAddress: c.config.Account.LocalCosmosAddress,
		PeerType:           c.config.Account.PeerType,
	})

	// Create client for the specific supernode
	supernodeClient, err := clientFactory.CreateClient(ctx, lumeraSupernode)
	if err != nil {
		c.logger.Error(ctx, "Failed to create supernode client", "address", supernodeAddress, "error", err)
		return nil, fmt.Errorf("failed to create supernode client: %w", err)
	}
	defer supernodeClient.Close(ctx)

	// Get the supernode status
	status, err := supernodeClient.GetSupernodeStatus(ctx)
	if err != nil {
		c.logger.Error(ctx, "Failed to get supernode status", "address", supernodeAddress, "error", err)
		return nil, fmt.Errorf("failed to get supernode status: %w", err)
	}

	c.logger.Info(ctx, "Successfully retrieved supernode status", "address", supernodeAddress)
	return status, nil
}

func (c *ClientImpl) DownloadCascade(ctx context.Context, actionID, outputDir, signature string) (string, error) {

	if actionID == "" {
		return "", fmt.Errorf("actionID is empty")
	}

	taskID, err := c.taskManager.CreateDownloadTask(ctx, actionID, outputDir, signature)
	if err != nil {
		return "", fmt.Errorf("create download task: %w", err)
	}

	c.logger.Info(ctx, "cascade download task created",
		"task_id", taskID,
		"action_id", actionID,
	)

	return taskID, nil
}
