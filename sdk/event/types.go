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
	SDKTaskStarted            EventType = "sdk:started"
	SDKSupernodesUnavailable  EventType = "sdk:supernodes_unavailable"
	SDKSupernodesFound        EventType = "sdk:supernodes_found"
	SDKRegistrationAttempt    EventType = "sdk:registration_attempt"
	SDKRegistrationFailure    EventType = "sdk:registration_failure"
	SDKRegistrationSuccessful EventType = "sdk:registration_successful"
	SDKTaskTxHashReceived     EventType = "sdk:txhash_received"
	SDKTaskCompleted          EventType = "sdk:completed"
	SDKTaskFailed             EventType = "sdk:failed"
)

const (
	SupernodeActionRetrieved   EventType = "supernode:action_retrieved"
	SupernodeActionFeeVerified EventType = "supernode:action_fee_verified"
	SupernodeTopCheckPassed    EventType = "supernode:top_check_passed"
	SupernodeMetadataDecoded   EventType = "supernode:metadata_decoded"
	SupernodeDataHashVerified  EventType = "supernode:data_hash_verified"
	SupernodeInputEncoded      EventType = "supernode:input_encoded"
	SupernodeSignatureVerified EventType = "supernode:signature_verified"
	SupernodeRQIDGenerated     EventType = "supernode:rqid_generated"
	SupernodeRQIDVerified      EventType = "supernode:rqid_verified"
	SupernodeArtefactsStored   EventType = "supernode:artefacts_stored"
	SupernodeActionFinalized   EventType = "supernode:action_finalized"
	SupernodeUnknown           EventType = "supernode:unknown"
)

// EventData is a map of event data attributes using standardized keys
type EventData map[EventDataKey]any

// Event represents an event emitted by the system
type Event struct {
	Type      EventType // Type of event
	TaskID    string    // ID of the task that emitted the event
	TaskType  string    // Type of task (CASCADE, SENSE)
	Timestamp time.Time // When the event occurred
	ActionID  string    // ID of the action associated with the task
	Data      EventData // Additional contextual data
}

// SupernodeData contains information about a supernode involved in an event
type SupernodeData struct {
	Supernode lumera.Supernode // The supernode involved
	Error     string           // Error message if applicable
}

func NewEvent(ctx context.Context, eventType EventType, taskID, taskType string, actionID string, data EventData) Event {
	if data == nil {
		data = make(EventData)
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
