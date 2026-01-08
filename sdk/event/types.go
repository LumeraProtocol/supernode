package event

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
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
	SDKConnectionEstablished  EventType = "sdk:connection_established"
	// Upload/processing phase events for cascade registration
	SDKUploadStarted     EventType = "sdk:upload_started"
	SDKUploadCompleted   EventType = "sdk:upload_completed"
	SDKUploadFailed      EventType = "sdk:upload_failed" // reason includes timeout
	SDKProcessingStarted EventType = "sdk:processing_started"
	SDKProcessingFailed  EventType = "sdk:processing_failed"
	SDKProcessingTimeout EventType = "sdk:processing_timeout"
	SDKMetadataICASigned EventType = "sdk:metadata_ica_signed"

	SDKDownloadAttempt   EventType = "sdk:download_attempt"
	SDKDownloadFailure   EventType = "sdk:download_failure"
	SDKDownloadStarted   EventType = "sdk:download_started"
	SDKDownloadCompleted EventType = "sdk:download_completed"
)

const (
	SupernodeActionRetrieved          EventType = "supernode:action_retrieved"
	SupernodeActionFeeVerified        EventType = "supernode:action_fee_verified"
	SupernodeTopCheckPassed           EventType = "supernode:top_check_passed"
	SupernodeMetadataDecoded          EventType = "supernode:metadata_decoded"
	SupernodeDataHashVerified         EventType = "supernode:data_hash_verified"
	SupernodeInputEncoded             EventType = "supernode:input_encoded"
	SupernodeSignatureVerified        EventType = "supernode:signature_verified"
	SupernodeRQIDGenerated            EventType = "supernode:rqid_generated"
	SupernodeRQIDVerified             EventType = "supernode:rqid_verified"
	SupernodeFinalizeSimulated        EventType = "supernode:finalize_simulated"
	SupernodeArtefactsStored          EventType = "supernode:artefacts_stored"
	SupernodeActionFinalized          EventType = "supernode:action_finalized"
	SupernodeArtefactsDownloaded      EventType = "supernode:artefacts_downloaded"
	SupernodeNetworkRetrieveStarted   EventType = "supernode:network_retrieve_started"
	SupernodeDecodeCompleted          EventType = "supernode:decode_completed"
	SupernodeServeReady               EventType = "supernode:serve_ready"
	SupernodeUnknown                  EventType = "supernode:unknown"
	SupernodeFinalizeSimulationFailed EventType = "supernode:finalize_simulation_failed"
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
