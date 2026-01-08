package logtrace

// Fields is a type alias for structured log fields
type Fields map[string]interface{}

// WithFields returns a copy of base with extra fields merged in.
func WithFields(base Fields, extra Fields) Fields {
	fields := Fields{}
	for key, value := range base {
		fields[key] = value
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

const (
	FieldCorrelationID  = "correlation_id"
	FieldOrigin         = "origin"
	FieldRole           = "role"
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
