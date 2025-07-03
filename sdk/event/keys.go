package event

// EventDataKey defines standard keys used in event data
type EventDataKey string

const (
	// Common data keys
	KeyError            EventDataKey = "error"
	KeyCount            EventDataKey = "count"
	KeySupernode        EventDataKey = "supernode"
	KeySupernodeAddress EventDataKey = "sn-address"
	KeyIteration        EventDataKey = "iteration"
	KeyTxHash           EventDataKey = "txhash"
	KeyMessage          EventDataKey = "message"
	KeyProgress         EventDataKey = "progress"
	KeyEventType        EventDataKey = "event_type"
	KeyOutputPath       EventDataKey = "output_path"

	// Task specific keys
	KeyTaskID   EventDataKey = "task_id"
	KeyActionID EventDataKey = "action_id"
)
