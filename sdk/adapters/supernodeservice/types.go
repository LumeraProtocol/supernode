package supernodeservice

import (
	"context"

	"github.com/LumeraProtocol/supernode/sdk/event"
)

type LoggerFunc func(
	ctx context.Context,
	eventType event.EventType,
	message string,
	data map[string]interface{},
)

type CascadeSupernodeRegisterRequest struct {
	Data        []byte
	ActionID    string
	TaskId      string
	EventLogger LoggerFunc
}

type CascadeSupernodeRegisterResponse struct {
	Success bool
	Message string
	TxHash  string
}
