package event

import (
	"action/adapters/lumera"
	"time"
)

// EventType represents the type of event
type EventType string

// Event types constants
const (
	// Task lifecycle events
	TaskStarted   EventType = "task.started"
	TaskCompleted EventType = "task.completed"
	TaskFailed    EventType = "task.failed"

	// Phase lifecycle events
	PhaseStarted   EventType = "phase.started"
	PhaseCompleted EventType = "phase.completed"
	PhaseFailed    EventType = "phase.failed"

	// Supernode events
	SupernodeAttempt   EventType = "supernode.attempt"
	SupernodeSucceeded EventType = "supernode.succeeded"
	SupernodeFailed    EventType = "supernode.failed"
)

// Event represents an event emitted by the system
type Event struct {
	Type      EventType              // Type of event
	TaskID    string                 // ID of the task that emitted the event
	TaskType  string                 // Type of task (CASCADE, SENSE)
	Timestamp time.Time              // When the event occurred
	Data      map[string]interface{} // Additional contextual data
}

// SupernodeData contains information about a supernode involved in an event
type SupernodeData struct {
	Supernode lumera.Supernode // The supernode involved
	Error     string           // Error message if applicable
}

func NewEvent(eventType EventType, taskID, taskType string, data map[string]interface{}) Event {
	if data == nil {
		data = make(map[string]interface{})
	}

	return Event{
		Type:      eventType,
		TaskID:    taskID,
		TaskType:  taskType,
		Timestamp: time.Now(),
		Data:      data,
	}
}
