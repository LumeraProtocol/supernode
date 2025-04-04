package log

import "context"

type Logger interface {
	Debug(ctx context.Context, msg string, keysAndValues ...interface{})

	Info(ctx context.Context, msg string, keysAndValues ...interface{})

	Warn(ctx context.Context, msg string, keysAndValues ...interface{})

	Error(ctx context.Context, msg string, keysAndValues ...interface{})
}
