package logtrace

// Fields is a type alias for structured log fields
type Fields map[string]interface{}

const (
	FieldCorrelationID  = "correlation_id"
	FieldMethod         = "method"
	FieldModule         = "module"
	FieldError          = "error"
	FieldStatus         = "status"
	FieldBlockHeight    = "block_height"
	FieldCreator        = "creator"
	FieldPrice          = "price"
	FieldSupernodeState = "supernode_state"
	FieldRequest        = "request"
	FieldStackTrace     = "stack_trace"
	FieldTxHash         = "tx_hash"
	FieldTaskID         = "task_id"
	FieldActionID       = "action_id"
	FieldHashHex        = "hash_hex"
	FieldActionState    = "action_state"
)
