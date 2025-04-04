package log

import "context"

var _ Logger = (*NoopLogger)(nil)

type NoopLogger struct{}

func NewNoopLogger() Logger {
	return &NoopLogger{}
}

func (l *NoopLogger) Debug(ctx context.Context, msg string, keysAndValues ...interface{}) {}

func (l *NoopLogger) Info(ctx context.Context, msg string, keysAndValues ...interface{}) {}

func (l *NoopLogger) Warn(ctx context.Context, msg string, keysAndValues ...interface{}) {}

func (l *NoopLogger) Error(ctx context.Context, msg string, keysAndValues ...interface{}) {}
