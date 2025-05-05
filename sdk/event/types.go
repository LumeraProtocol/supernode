package event

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
)

// EventType represents the type of event emitted by the system
type EventType string

// Event types emitted by the system
// These events are used to track the progress of tasks
// and to notify subscribers about important changes in the system.
const (
	TaskStarted                          EventType = "task.started"
	TaskProgressActionVerified           EventType = "task.progress.action_verified"
	TaskProgressActionVerificationFailed EventType = "task.progress.action_verification_failed"
	TaskProgressSupernodesFound          EventType = "task.progress.supernode_found"
	TaskProgressSupernodesUnavailable    EventType = "task.progress.supernodes_unavailable"
	TaskProgressRegistrationInProgress   EventType = "task.progress.registration_in_progress"
	TaskProgressRegistrationFailure      EventType = "task.progress.registration_failure"
	TaskProgressRegistrationSuccessful   EventType = "task.progress.registration_successful"
	TaskCompleted                        EventType = "task.completed"
	TxhasReceived                        EventType = "txhash.received"
	TaskFailed                           EventType = "task.failed"
)

// Task progress steps in order
// This is the order in which events are expected to occur
// during the task lifecycle. It is used to track progress.
// The order of events in this slice should match the order
// in which they are expected to occur in the task lifecycle.
// The index of each event in this slice represents its
// position in the task lifecycle. The first event in the slice is the
// first event that should be emitted when a task starts.
var taskProgressSteps = []EventType{
	TaskStarted,
	TaskProgressActionVerified,
	TaskProgressSupernodesFound,
	TaskProgressRegistrationInProgress,
	TaskCompleted,
}

// Event represents an event emitted by the system
type Event struct {
	Type      EventType              // Type of event
	TaskID    string                 // ID of the task that emitted the event
	TaskType  string                 // Type of task (CASCADE, SENSE)
	Timestamp time.Time              // When the event occurred
	ActionID  string                 // ID of the action associated with the task
	Data      map[string]interface{} // Additional contextual data
}

// SupernodeData contains information about a supernode involved in an event
type SupernodeData struct {
	Supernode lumera.Supernode // The supernode involved
	Error     string           // Error message if applicable
}

func NewEvent(ctx context.Context, eventType EventType, taskID, taskType string, actionID string, data map[string]interface{}) Event {
	if data == nil {
		data = make(map[string]interface{})
	}

	return Event{
		Type:      eventType,
		TaskID:    taskID,
		TaskType:  taskType,
		Timestamp: time.Now(),
		Data:      data,
		ActionID:  actionID,
	}
}

// GetTaskProgress returns current progress as (y, x), where y = current step number, x = total steps.
func GetTaskProgress(current EventType) (int, int) {
	for idx, step := range taskProgressSteps {
		if step == current {
			return idx + 1, len(taskProgressSteps)
		}
	}
	// Unknown event, treat as 0 progress
	return 0, len(taskProgressSteps)
}
