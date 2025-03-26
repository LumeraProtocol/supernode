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

	// Cascade-specific events
	CascadeActionValidationStarted  EventType = "cascade.action_validation.started"
	CascadeActionValidationComplete EventType = "cascade.action_validation.complete"
	CascadeActionValidationFailed   EventType = "cascade.action_validation.failed"

	CascadeSupernodeSelectionStarted  EventType = "cascade.supernode_selection.started"
	CascadeSupernodeSelectionComplete EventType = "cascade.supernode_selection.complete"
	CascadeSupernodeSelectionFailed   EventType = "cascade.supernode_selection.failed"

	CascadeUploadStarted      EventType = "cascade.upload.started"
	CascadeUploadComplete     EventType = "cascade.upload.complete"
	CascadeUploadFailed       EventType = "cascade.upload.failed"
	CascadeSupernodeAttempt   EventType = "cascade.supernode.attempt"
	CascadeSupernodeSucceeded EventType = "cascade.supernode.succeeded"
	CascadeSupernodeFailed    EventType = "cascade.supernode.failed"
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

// NewEvent creates a new event with the current timestamp
func NewEvent(eventType EventType, taskID, taskType string, data map[string]interface{}) Event {
	return Event{
		Type:      eventType,
		TaskID:    taskID,
		TaskType:  taskType,
		Timestamp: time.Now(),
		Data:      data,
	}
}
