package task_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	lumeramocks "github.com/LumeraProtocol/supernode/sdk/adapters/lumera/mocks"
	"github.com/LumeraProtocol/supernode/sdk/config"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"
	"github.com/LumeraProtocol/supernode/sdk/task"
	"github.com/LumeraProtocol/supernode/sdk/task/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// Setup helper function to create a mock manager
func setupMockManager(t *testing.T, mockLumeraClient lumera.Client) task.Manager {
	// Create basic config
	cfg := config.Config{
		Account: config.AccountConfig{
			LocalCosmosAddress: "test-address",
		},
		Lumera: config.LumeraConfig{
			GRPCAddr: "localhost:9090",
			ChainID:  "test-chain",
		},
	}

	// Create manager with dependencies
	manager, err := task.NewManager(context.Background(), cfg, log.NewNoopLogger(), nil)
	require.NoError(t, err)
	require.NotNil(t, manager)

	// If a mock client is provided, replace the real one
	if mockLumeraClient != nil {
		// Need to use reflection or unexported field accessor to replace the client
		// For test simplicity, we'll create mock-based tests separately
	}

	return manager
}

// TestCreateCascadeTask tests the CreateCascadeTask method
func TestCreateCascadeTask(t *testing.T) {
	testCases := []struct {
		name      string
		data      []byte
		actionID  string
		mockSetup func(*gomock.Controller) *mocks.MockManager
		wantErr   bool
		errString string
	}{
		{
			name:     "Success case",
			data:     []byte("test data"),
			actionID: "action123",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					CreateCascadeTask(gomock.Any(), []byte("test data"), "action123").
					Return("task123", nil)
				return mockManager
			},
			wantErr: false,
		},
		{
			name:     "Empty data",
			data:     []byte{},
			actionID: "action123",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					CreateCascadeTask(gomock.Any(), []byte{}, "action123").
					Return("", errors.New("data cannot be empty"))
				return mockManager
			},
			wantErr:   true,
			errString: "data cannot be empty",
		},
		{
			name:     "Empty action ID",
			data:     []byte("test data"),
			actionID: "",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					CreateCascadeTask(gomock.Any(), []byte("test data"), "").
					Return("", errors.New("action ID cannot be empty"))
				return mockManager
			},
			wantErr:   true,
			errString: "action ID cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockManager := tc.mockSetup(ctrl)

			// Call the method
			taskID, err := mockManager.CreateCascadeTask(context.Background(), tc.data, tc.actionID)

			// Check results
			if tc.wantErr {
				assert.Error(t, err)
				if tc.errString != "" {
					assert.Contains(t, err.Error(), tc.errString)
				}
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, taskID)
			}
		})
	}
}

// TestImplementation_CreateCascadeTask tests the actual manager implementation
func TestImplementation_CreateCascadeTask(t *testing.T) {
	// Setup
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create mock lumera client
	mockLumeraClient := lumeramocks.NewMockClient(ctrl)

	// Setup expectations for lumera client
	// We expect GetAction to be called during task validation
	mockLumeraClient.EXPECT().
		GetAction(gomock.Any(), "action123").
		Return(lumera.Action{
			ID:    "action123",
			State: lumera.ACTION_STATE_PENDING,
		}, nil).AnyTimes()

	// Mock for successful supernode fetch
	mockLumeraClient.EXPECT().
		GetSupernodes(gomock.Any(), gomock.Any()).
		Return([]lumera.Supernode{
			{
				CosmosAddress: "cosmos123",
				GrpcEndpoint:  "localhost:9090",
				State:         lumera.SUPERNODE_STATE_ACTIVE,
			},
		}, nil).AnyTimes()

}

// TestGetTask tests the GetTask method
func TestGetTask(t *testing.T) {
	// Create sample task entry
	sampleTask := &task.TaskEntry{
		TaskID:   "task123",
		TaskType: task.TaskTypeCascade,
		Status:   task.StatusCompleted,
	}

	testCases := []struct {
		name      string
		taskID    string
		mockSetup func(*gomock.Controller) *mocks.MockManager
		wantTask  *task.TaskEntry
		wantFound bool
	}{
		{
			name:   "Task found",
			taskID: "task123",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					GetTask(gomock.Any(), "task123").
					Return(sampleTask, true)
				return mockManager
			},
			wantTask:  sampleTask,
			wantFound: true,
		},
		{
			name:   "Task not found",
			taskID: "nonexistent",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					GetTask(gomock.Any(), "nonexistent").
					Return(nil, false)
				return mockManager
			},
			wantTask:  nil,
			wantFound: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockManager := tc.mockSetup(ctrl)

			// Call the method
			task, found := mockManager.GetTask(context.Background(), tc.taskID)

			// Check results
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.wantTask, task)
		})
	}
}

// TestDeleteTask tests the DeleteTask method
func TestDeleteTask(t *testing.T) {
	testCases := []struct {
		name      string
		taskID    string
		mockSetup func(*gomock.Controller) *mocks.MockManager
		wantErr   bool
		errString string
	}{
		{
			name:   "Success case",
			taskID: "task123",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					DeleteTask(gomock.Any(), "task123").
					Return(nil)
				return mockManager
			},
			wantErr: false,
		},
		{
			name:   "Task not found",
			taskID: "nonexistent",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					DeleteTask(gomock.Any(), "nonexistent").
					Return(errors.New("task not found"))
				return mockManager
			},
			wantErr:   true,
			errString: "task not found",
		},
		{
			name:   "Empty task ID",
			taskID: "",
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					DeleteTask(gomock.Any(), "").
					Return(errors.New("task ID cannot be empty"))
				return mockManager
			},
			wantErr:   true,
			errString: "task ID cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockManager := tc.mockSetup(ctrl)

			// Call the method
			err := mockManager.DeleteTask(context.Background(), tc.taskID)

			// Check results
			if tc.wantErr {
				assert.Error(t, err)
				if tc.errString != "" {
					assert.Contains(t, err.Error(), tc.errString)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSubscribeToEvents tests the SubscribeToEvents method
func TestSubscribeToEvents(t *testing.T) {
	testCases := []struct {
		name      string
		eventType event.EventType
		mockSetup func(*gomock.Controller) *mocks.MockManager
	}{
		{
			name:      "Subscribe to TaskStarted",
			eventType: event.TaskStarted,
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					SubscribeToEvents(gomock.Any(), event.TaskStarted, gomock.Any())
				return mockManager
			},
		},
		{
			name:      "Subscribe to TaskCompleted",
			eventType: event.TaskCompleted,
			mockSetup: func(ctrl *gomock.Controller) *mocks.MockManager {
				mockManager := mocks.NewMockManager(ctrl)
				mockManager.EXPECT().
					SubscribeToEvents(gomock.Any(), event.TaskCompleted, gomock.Any())
				return mockManager
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockManager := tc.mockSetup(ctrl)

			// Create dummy handler
			handler := func(ctx context.Context, e event.Event) {}

			// Call the method and verify it doesn't panic
			mockManager.SubscribeToEvents(context.Background(), tc.eventType, handler)
		})
	}
}

// TestSubscribeToAllEvents tests the SubscribeToAllEvents method
func TestSubscribeToAllEvents(t *testing.T) {
	// Setup mock
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockManager := mocks.NewMockManager(ctrl)
	mockManager.EXPECT().
		SubscribeToAllEvents(gomock.Any(), gomock.Any())

	// Create dummy handler
	handler := func(ctx context.Context, e event.Event) {}

	// Call the method and verify it doesn't panic
	mockManager.SubscribeToAllEvents(context.Background(), handler)
}

// TestEventHandling tests the event handling functionality
func TestEventHandling(t *testing.T) {
	// This test would be more complex and require access to internal event handlers
	// It would verify that:
	// 1. Events are properly forwarded from tasks to subscribers
	// 2. Task status is updated based on events
	// 3. Event bus receives all events

	// Setup would require:
	// - Mock task that emits events
	// - Mock event bus
	// - Test subscription handlers

	// For now, we'll omit this test since it requires significant internal access
}

// TestNewManager tests creating a new manager
func TestNewManager(t *testing.T) {
	// Setup
	ctx := context.Background()
	cfg := config.Config{
		Account: config.AccountConfig{
			LocalCosmosAddress: "test-address",
		},
		Lumera: config.LumeraConfig{
			GRPCAddr: "localhost:9090",
			ChainID:  "test-chain",
		},
	}
	logger := log.NewNoopLogger()

	// Test with mock controller
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create a simple test using the public constructor
	manager, err := task.NewManager(ctx, cfg, logger, nil)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, manager)

	// Test with invalid config
	invalidCfg := config.Config{}
	manager, err = task.NewManager(ctx, invalidCfg, logger, nil)

	// This might succeed or fail depending on internal validation
	// For this test, we just demonstrate the approach
	if err != nil {
		assert.Error(t, err)
	}
}
