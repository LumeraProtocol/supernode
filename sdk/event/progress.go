package event

// ProgressInfo provides comprehensive progress information
type ProgressInfo struct {
	Percentage   int        // Percentage complete (0-100)
	Status       TaskStatus // Current status (pending, active, completed, failed)
	CurrentEvent EventType  // The current event type
	IsErrorState bool       // Whether this is an error state

}

// TaskStatus represents the current status of a task
type TaskStatus string

const (
	StatusPending   TaskStatus = "PENDING"
	StatusActive    TaskStatus = "ACTIVE"
	StatusCompleted TaskStatus = "COMPLETED"
	StatusFailed    TaskStatus = "FAILED"
)

// eventProgressMap maps event types to their progress percentages
var eventProgressMap = map[EventType]int{
	//SDK
	SDKTaskStarted:           0,
	SDKSupernodesUnavailable: 5,
	SDKSupernodesFound:       10,
	SDKRegistrationAttempt:   12,
	SDKRegistrationFailure:   12,
	//Supernode
	SupernodeActionRetrieved:   15,
	SupernodeActionFeeVerified: 20,
	SupernodeTopCheckPassed:    25,
	SupernodeMetadataDecoded:   30,
	SupernodeDataHashVerified:  35,
	SupernodeInputEncoded:      40,
	SupernodeSignatureVerified: 45,
	SupernodeRQIDGenerated:     50,
	SupernodeRQIDVerified:      55,
	SupernodeArtefactsStored:   60,
	SupernodeActionFinalized:   80,
	// SDK
	SDKTaskTxHashReceived:     97,
	SDKRegistrationSuccessful: 99,
	SDKTaskCompleted:          100,
	SDKTaskFailed:             100,
}

// GetProgressFromEvent calculates progress information from a single event
func GetProgressFromEvent(e EventType) ProgressInfo {
	// Determine percentage
	percentage := 0
	if p, exists := eventProgressMap[e]; exists {
		percentage = p
	}

	// Determine status
	var status TaskStatus
	isError := false

	switch e {
	case SDKTaskStarted:
		status = StatusActive
	case SDKTaskCompleted:
		status = StatusCompleted
	case SDKTaskFailed:
		status = StatusFailed
		isError = true
	default:
		status = StatusActive
	}

	return ProgressInfo{
		Percentage:   percentage,
		Status:       status,
		CurrentEvent: e,
		IsErrorState: isError,
	}
}

// GetLatestProgress calculates progress info from the most recent event in a list
func GetLatestProgress(events []Event) ProgressInfo {
	if len(events) == 0 {
		return ProgressInfo{
			Percentage:   0,
			Status:       StatusPending,
			CurrentEvent: "",
			IsErrorState: false,
		}
	}

	// Return progress based on the latest event
	return GetProgressFromEvent(events[len(events)-1].Type)
}
