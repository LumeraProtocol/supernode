package action

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/LumeraProtocol/supernode/sdk/config"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"
	"github.com/LumeraProtocol/supernode/sdk/task"

	mocktask "github.com/LumeraProtocol/supernode/sdk/task/testutil/mocks"
)

func newClientWithMock(mgr *mocktask.Manager) *ClientImpl {
	return &ClientImpl{
		config:      config.Config{},     // empty fixture
		taskManager: mgr,                 // injected mock
		logger:      log.NewNoopLogger(), // quiet logger
	}
}

func TestClient_StartCascade(t *testing.T) {
	ctx := context.Background()
	payload := []byte("hello-world")
	actionID := "act-123"
	mockErr := errors.New("boom")

	tests := map[string]struct {
		data         []byte
		actionID     string
		setupMock    func(*mocktask.Manager)
		wantTaskID   string
		wantErrMatch func(error) bool
	}{
		"happy path": {
			data:     payload,
			actionID: actionID,
			setupMock: func(m *mocktask.Manager) {
				m.On("CreateCascadeTask", ctx, payload, actionID).
					Return("task-42", nil)
			},
			wantTaskID:   "task-42",
			wantErrMatch: func(err error) bool { return err == nil },
		},
		"empty action id": {
			data:         payload,
			actionID:     "",
			setupMock:    func(*mocktask.Manager) {},
			wantErrMatch: func(err error) bool { return errors.Is(err, ErrEmptyActionID) },
		},
		"empty data": {
			data:         nil,
			actionID:     actionID,
			setupMock:    func(*mocktask.Manager) {},
			wantErrMatch: func(err error) bool { return errors.Is(err, ErrEmptyData) },
		},
		"manager failure": {
			data:     payload,
			actionID: actionID,
			setupMock: func(m *mocktask.Manager) {
				m.On("CreateCascadeTask", ctx, payload, actionID).
					Return("", mockErr)
			},
			wantErrMatch: func(err error) bool {
				return err != nil && errors.Is(err, mockErr)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mockMgr := mocktask.NewManager(t)
			tc.setupMock(mockMgr)

			client := newClientWithMock(mockMgr)

			gotID, err := client.StartCascade(ctx, tc.data, tc.actionID)
			assert.True(t, tc.wantErrMatch(err), "error assertion failed")
			assert.Equal(t, tc.wantTaskID, gotID)
		})
	}
}

func TestClient_GetTask(t *testing.T) {
	ctx := context.Background()
	entry := &task.TaskEntry{TaskID: "tid-7"}

	tests := map[string]struct {
		taskID    string
		setupMock func(*mocktask.Manager)
		wantEntry *task.TaskEntry
		wantFound bool
	}{
		"found": {
			taskID: "tid-7",
			setupMock: func(m *mocktask.Manager) {
				m.On("GetTask", ctx, "tid-7").Return(entry, true)
			},
			wantEntry: entry, wantFound: true,
		},
		"missing": {
			taskID: "tid-404",
			setupMock: func(m *mocktask.Manager) {
				m.On("GetTask", ctx, "tid-404").Return((*task.TaskEntry)(nil), false)
			},
			wantEntry: nil, wantFound: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mockMgr := mocktask.NewManager(t)
			tc.setupMock(mockMgr)

			client := newClientWithMock(mockMgr)
			got, ok := client.GetTask(ctx, tc.taskID)

			assert.Equal(t, tc.wantFound, ok)
			assert.Equal(t, tc.wantEntry, got)
		})
	}
}

func TestClient_DeleteTask(t *testing.T) {
	ctx := context.Background()
	mockErr := errors.New("delete fail")

	tests := map[string]struct {
		taskID     string
		setupMock  func(*mocktask.Manager)
		wantErrNil bool
	}{
		"happy path": {
			taskID: "tid-1",
			setupMock: func(m *mocktask.Manager) {
				m.On("DeleteTask", ctx, "tid-1").Return(nil)
			},
			wantErrNil: true,
		},
		"empty id": {
			taskID:     "",
			setupMock:  func(*mocktask.Manager) {},
			wantErrNil: false,
		},
		"manager error": {
			taskID: "tid-2",
			setupMock: func(m *mocktask.Manager) {
				m.On("DeleteTask", ctx, "tid-2").Return(mockErr)
			},
			wantErrNil: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mockMgr := mocktask.NewManager(t)
			tc.setupMock(mockMgr)

			client := newClientWithMock(mockMgr)
			err := client.DeleteTask(ctx, tc.taskID)

			assert.Equal(t, tc.wantErrNil, err == nil)
		})
	}
}

func TestClient_Subscribe(t *testing.T) {
	ctx := context.Background()
	handler := func(context.Context, event.Event) {}

	mockMgr := mocktask.NewManager(t)

	mockMgr.
		On("SubscribeToEvents", ctx, event.EventType("foo"), mock.AnythingOfType("event.Handler")).
		Once()
	mockMgr.
		On("SubscribeToAllEvents", ctx, mock.AnythingOfType("event.Handler")).
		Once()

	client := newClientWithMock(mockMgr)

	assert.NoError(t, client.SubscribeToEvents(ctx, "foo", handler))
	assert.NoError(t, client.SubscribeToAllEvents(ctx, handler))

	// nil Manager branch
	nilClient := &ClientImpl{}
	assert.Error(t, nilClient.SubscribeToEvents(ctx, "bar", handler))
	assert.Error(t, nilClient.SubscribeToAllEvents(ctx, handler))
}
