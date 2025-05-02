package log

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/log/hooks"
)

type (
	// private type used to define context keys
	ctxKey    int
	serverKey string
	taskID    string
)

var (
	ip = ""
)

const (
	// PrefixKey is the prefix of the log record
	PrefixKey ctxKey = iota
	// ServerKey is prefix of server ip
	ServerKey serverKey = "server"
	// TaskIDKey is prefix of task id
	TaskIDKey taskID = "task_id"
)

// ContextWithPrefix returns a new context with PrefixKey value.
func ContextWithPrefix(ctx context.Context, prefix string) context.Context {

	ctx = ContextWithServer(ctx, ip)

	return context.WithValue(ctx, PrefixKey, prefix)
}

// ContextWithServer returns a new context with ServerKey value.
func ContextWithServer(ctx context.Context, server string) context.Context {
	return context.WithValue(ctx, ServerKey, server)
}

func init() {
	AddHook(hooks.NewContextHook(PrefixKey, func(ctxValue interface{}, msg string, fields hooks.ContextHookFields) (string, hooks.ContextHookFields) {
		fields["prefix"] = ctxValue
		return msg, fields
	}))
}
