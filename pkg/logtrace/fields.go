package logtrace

// Fields is a type alias for structured log fields
type Fields map[string]interface{}

const (
	FieldCorrelationID           = "correlation_id"
	FieldMethod                  = "method"
	FieldModule                  = "module"
	FieldError                   = "error"
	FieldStatus                  = "status"
	FieldBlockHeight             = "block_height"
	FieldLimit                   = "limit"
	FieldSupernodeState          = "supernode_state"
	FieldRequest                 = "request"
	FieldSupernodeAccountAddress = "supernode_account_address"
	FieldIsPrimary               = "is_primary"
	FieldStackTrace              = "stack_trace"

	ValueLumeraSDK      = "lumera-sdk"
	ValueActionSDK      = "action-sdk"
	ValueTransaction    = "cosmos-tx"
	ValueBaseTendermint = "tendermint"
)
