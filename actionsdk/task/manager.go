package task

// TODO: Implement task cleanup and retention policies

import (
	"context"
	"fmt"
	"sync"

	"action/adapters/lumera"
	"action/config"

	lumeraclient "github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/google/uuid"
)

// Manager handles task creation and management
type Manager interface {
	// CreateSenseTask creates and starts a Sense task
	CreateSenseTask(ctx context.Context, fileHash, actionID, filePath string) (string, error)

	// CreateCascadeTask creates and starts a Cascade task
	CreateCascadeTask(ctx context.Context, fileHash, actionID, filePath string) (string, error)

	// GetTask retrieves a task by its ID
	GetTask(taskID string) (Task, bool)
}

// ManagerImpl implements the Manager interface
type ManagerImpl struct {
	client     lumera.Client
	config     config.Config
	tasks      map[string]Task
	tasksMutex sync.RWMutex
}

// NewManager creates a new task manager
func NewManager(client lumeraclient.Client, config config.Config) Manager {
	// Adapt the lumera.Client to our Client interface
	clientAdapter := lumera.NewAdapter(client)

	return &ManagerImpl{
		client: clientAdapter,
		config: config,
		tasks:  make(map[string]Task),
	}
}

// CreateSenseTask creates and starts a Sense task
func (m *ManagerImpl) CreateSenseTask(
	ctx context.Context,
	fileHash string,
	actionID string,
	filePath string,
) (string, error) {
	// Generate task ID
	taskID := uuid.New().String()

	// Create task with config
	task := NewSenseTask(taskID, fileHash, actionID, filePath, m.client, m.config)

	// Store task
	m.tasksMutex.Lock()
	m.tasks[taskID] = task
	m.tasksMutex.Unlock()

	// Start task asynchronously
	go func() {
		err := task.Run(ctx)
		if err != nil {
			// Task will update its own error state
			fmt.Printf("Sense task %s failed: %v\n", taskID, err)
		}
	}()

	return taskID, nil
}

// CreateCascadeTask creates and starts a Cascade task
func (m *ManagerImpl) CreateCascadeTask(
	ctx context.Context,
	fileHash string,
	actionID string,
	filePath string,
) (string, error) {
	// Generate task ID
	taskID := uuid.New().String()

	// Create task with config
	task := NewCascadeTask(taskID, fileHash, actionID, filePath, m.client, m.config)

	// Store task
	m.tasksMutex.Lock()
	m.tasks[taskID] = task
	m.tasksMutex.Unlock()

	// Start task asynchronously
	go func() {
		err := task.Run(ctx)
		if err != nil {
			// Task will update its own error state
			fmt.Printf("Cascade task %s failed: %v\n", taskID, err)
		}
	}()

	return taskID, nil
}

// GetTask retrieves a task by its ID
func (m *ManagerImpl) GetTask(taskID string) (Task, bool) {
	m.tasksMutex.RLock()
	defer m.tasksMutex.RUnlock()

	task, exists := m.tasks[taskID]
	return task, exists
}
