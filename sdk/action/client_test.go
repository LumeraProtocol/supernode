package action_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LumeraProtocol/supernode/sdk/action"
	"github.com/LumeraProtocol/supernode/sdk/action/mocks"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/task"
	taskmocks "github.com/LumeraProtocol/supernode/sdk/task/mocks"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// TestStartCascade tests the StartCascade method
func TestStartCascade(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name      string
		data      []byte
		actionID  string
		mockSetup func(*gomock.Controller) action.Client
		wantErr   bool
		errType   error
		wantID    string
	}{
		{
			name:     "Success case",
			data:     []byte("test data"),
			actionID: "action123",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					StartCascade(gomock.Any(), []byte("test data"), "action123").
					Return("task123", nil)
				return mockClient
			},
			wantErr: false,
			wantID:  "task123",
		},
		{
			name:     "Empty action ID",
			data:     []byte("test data"),
			actionID: "",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					StartCascade(gomock.Any(), []byte("test data"), "").
					Return("", action.ErrEmptyActionID)
				return mockClient
			},
			wantErr: true,
			errType: action.ErrEmptyActionID,
		},
		{
			name:     "Empty data",
			data:     []byte{},
			actionID: "action123",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					StartCascade(gomock.Any(), []byte{}, "action123").
					Return("", action.ErrEmptyData)
				return mockClient
			},
			wantErr: true,
			errType: action.ErrEmptyData,
		},
		{
			name:     "Task manager error",
			data:     []byte("test data"),
			actionID: "action123",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					StartCascade(gomock.Any(), []byte("test data"), "action123").
					Return("", errors.New("task manager error"))
				return mockClient
			},
			wantErr: true,
		},
	}

	// Run the test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create client with mocked behavior
			client := tc.mockSetup(ctrl)

			// Call the method
			taskID, err := client.StartCascade(context.Background(), tc.data, tc.actionID)

			// Check expectations
			if tc.wantErr {
				assert.Error(t, err)
				if tc.errType != nil {
					assert.ErrorIs(t, err, tc.errType)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.wantID, taskID)
			}
		})
	}
}

// TestDeleteTask tests the DeleteTask method
func TestDeleteTask(t *testing.T) {
	testCases := []struct {
		name      string
		taskID    string
		mockSetup func(*gomock.Controller) action.Client
		wantErr   bool
	}{
		{
			name:   "Success case",
			taskID: "task123",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					DeleteTask(gomock.Any(), "task123").
					Return(nil)
				return mockClient
			},
			wantErr: false,
		},
		{
			name:   "Empty task ID",
			taskID: "",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					DeleteTask(gomock.Any(), "").
					Return(errors.New("task ID cannot be empty"))
				return mockClient
			},
			wantErr: true,
		},
		{
			name:   "Task manager error",
			taskID: "task123",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					DeleteTask(gomock.Any(), "task123").
					Return(errors.New("task manager error"))
				return mockClient
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create client with mocked behavior
			client := tc.mockSetup(ctrl)

			// Call the method
			err := client.DeleteTask(context.Background(), tc.taskID)

			// Check expectations
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGetTask tests the GetTask method
func TestGetTask(t *testing.T) {
	// Create a sample task entry for testing
	sampleTask := &task.TaskEntry{
		TaskID:   "task123",
		TaskType: task.TaskTypeCascade,
		Status:   task.StatusCompleted,
	}

	testCases := []struct {
		name      string
		taskID    string
		mockSetup func(*gomock.Controller) action.Client
		wantTask  *task.TaskEntry
		wantFound bool
	}{
		{
			name:   "Task found",
			taskID: "task123",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					GetTask(gomock.Any(), "task123").
					Return(sampleTask, true)
				return mockClient
			},
			wantTask:  sampleTask,
			wantFound: true,
		},
		{
			name:   "Task not found",
			taskID: "nonexistent",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					GetTask(gomock.Any(), "nonexistent").
					Return(nil, false)
				return mockClient
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

			// Create client with mocked behavior
			client := tc.mockSetup(ctrl)

			// Call the method
			task, found := client.GetTask(context.Background(), tc.taskID)

			// Check expectations
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.wantTask, task)
		})
	}
}

// TestSubscribeToEvents tests the SubscribeToEvents method
func TestSubscribeToEvents(t *testing.T) {
	testCases := []struct {
		name      string
		eventType event.EventType
		mockSetup func(*gomock.Controller) action.Client
		wantErr   bool
	}{
		{
			name:      "Subscribe successfully",
			eventType: event.TaskStarted,
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					SubscribeToEvents(gomock.Any(), event.TaskStarted, gomock.Any()).
					Return(nil)
				return mockClient
			},
			wantErr: false,
		},
		{
			name:      "Task manager error",
			eventType: event.TaskStarted,
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					SubscribeToEvents(gomock.Any(), event.TaskStarted, gomock.Any()).
					Return(errors.New("task manager is nil"))
				return mockClient
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create client with mocked behavior
			client := tc.mockSetup(ctrl)

			// Create a dummy handler
			handler := func(ctx context.Context, e event.Event) {}

			// Call the method
			err := client.SubscribeToEvents(context.Background(), tc.eventType, handler)

			// Check expectations
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSubscribeToAllEvents tests the SubscribeToAllEvents method
func TestSubscribeToAllEvents(t *testing.T) {
	testCases := []struct {
		name      string
		mockSetup func(*gomock.Controller) action.Client
		wantErr   bool
	}{
		{
			name: "Subscribe successfully",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					SubscribeToAllEvents(gomock.Any(), gomock.Any()).
					Return(nil)
				return mockClient
			},
			wantErr: false,
		},
		{
			name: "Task manager error",
			mockSetup: func(ctrl *gomock.Controller) action.Client {
				mockClient := mocks.NewMockClient(ctrl)
				mockClient.EXPECT().
					SubscribeToAllEvents(gomock.Any(), gomock.Any()).
					Return(errors.New("task manager is nil"))
				return mockClient
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create client with mocked behavior
			client := tc.mockSetup(ctrl)

			// Create a dummy handler
			handler := func(ctx context.Context, e event.Event) {}

			// Call the method
			err := client.SubscribeToAllEvents(context.Background(), handler)

			// Check expectations
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestClientImpl_StartCascade tests the StartCascade method of ClientImpl using mocked dependencies
func TestClientImpl_StartCascade(t *testing.T) {
	testCases := []struct {
		name             string
		data             []byte
		actionID         string
		setupTaskManager func(*taskmocks.MockManager)
		expectError      bool
		expectedErr      error
		expectedTaskID   string
	}{
		{
			name:     "Successful cascade creation",
			data:     []byte("test-data"),
			actionID: "valid-action-id",
			setupTaskManager: func(mockTaskManager *taskmocks.MockManager) {
				mockTaskManager.EXPECT().
					CreateCascadeTask(gomock.Any(), []byte("test-data"), "valid-action-id").
					Return("task-123", nil)
			},
			expectError:    false,
			expectedTaskID: "task-123",
		},
		{
			name:     "Empty action ID validation",
			data:     []byte("test-data"),
			actionID: "",
			setupTaskManager: func(mockTaskManager *taskmocks.MockManager) {
				// Task manager should not be called for empty action ID
			},
			expectError: true,
			expectedErr: action.ErrEmptyActionID,
		},
		{
			name:     "Empty data validation",
			data:     []byte{},
			actionID: "valid-action-id",
			setupTaskManager: func(mockTaskManager *taskmocks.MockManager) {
				// Task manager should not be called for empty data
			},
			expectError: true,
			expectedErr: action.ErrEmptyData,
		},
		{
			name:     "Task manager error",
			data:     []byte("test-data"),
			actionID: "valid-action-id",
			setupTaskManager: func(mockTaskManager *taskmocks.MockManager) {
				mockTaskManager.EXPECT().
					CreateCascadeTask(gomock.Any(), []byte("test-data"), "valid-action-id").
					Return("", errors.New("failed to create task"))
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock controller
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create mocked task manager
			mockTaskManager := taskmocks.NewMockManager(ctrl)
			tc.setupTaskManager(mockTaskManager)

			// Create test client using exported fields
			testClient := action.NewClientForTesting(mockTaskManager)

			// Call the method
			taskID, err := testClient.StartCascade(context.Background(), tc.data, tc.actionID)

			// Verify results
			if tc.expectError {
				assert.Error(t, err)
				if tc.expectedErr != nil {
					assert.ErrorIs(t, err, tc.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedTaskID, taskID)
			}
		})
	}
}
