package logtrace

import (
	"context"
	"os"
	"runtime"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ContextKey is the type used for storing values in context
type ContextKey string

// CorrelationIDKey is the key for storing correlation ID in context
const CorrelationIDKey ContextKey = "correlation_id"

var logger *zap.Logger

// Setup initializes the logger for readable output in all modes.
func Setup(serviceName string) {
	var err error

	// Always start with the development config for colored, readable logs.
	config := zap.NewDevelopmentConfig()

	// Ensure the log level is always colorized.
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder

	tracingEnabled := getTracingEnabled()
	config.EncoderConfig.TimeKey = "" // Remove the timestamp
	config.DisableCaller = true

	// Always respect the LOG_LEVEL environment variable.
	config.Level = zap.NewAtomicLevelAt(getLogLevel())

	// Build the logger from the customized config.
	if tracingEnabled {
		logger, err = config.Build(zap.AddCallerSkip(1), zap.AddStacktrace(zapcore.ErrorLevel))
	} else {
		logger, err = config.Build()
	}
	if err != nil {
		panic(err)
	}
}

// getLogLevel returns the log level from environment variable LOG_LEVEL
func getLogLevel() zapcore.Level {
	levelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch levelStr {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

// getTracingEnabled returns whether tracing is enabled from LOG_TRACING env var
func getTracingEnabled() bool {
	return strings.ToLower(os.Getenv("LOG_TRACING")) == "1"
}

// CtxWithCorrelationID stores a correlation ID inside the context
func CtxWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, CorrelationIDKey, correlationID)
}

// extractCorrelationID retrieves the correlation ID from context
func extractCorrelationID(ctx context.Context) string {
	if correlationID, ok := ctx.Value(CorrelationIDKey).(string); ok {
		return correlationID
	}
	return "unknown"
}

// logWithLevel logs a message with structured fields.
func logWithLevel(level zapcore.Level, ctx context.Context, message string, fields Fields) {
	if logger == nil {
		Setup("unknown-service") // Fallback if Setup wasn't called
	}

	// Always enrich logs with the correlation ID.
	// allFields := make(Fields, len(fields)+1)
	// for k, v := range fields {
	// 	allFields[k] = v
	// }
	// allFields[FieldCorrelationID] = extractCorrelationID(ctx)

	// Convert the map to a slice of zap.Field
	zapFields := make([]zap.Field, 0, len(fields))
	for k, v := range fields {
		zapFields = append(zapFields, zap.Any(k, v))
	}

	// Add stack trace if tracing is enabled
	if getTracingEnabled() {
		// Get caller information
		if pc, file, line, ok := runtime.Caller(2); ok {
			fn := runtime.FuncForPC(pc)
			if fn != nil {
				zapFields = append(zapFields, zap.String("caller", fn.Name()))
				zapFields = append(zapFields, zap.String("file", file))
				zapFields = append(zapFields, zap.Int("line", line))
			}
		}
	}

	// Log with the structured fields.
	switch level {
	case zapcore.DebugLevel:
		logger.Debug(message, zapFields...)
	case zapcore.InfoLevel:
		logger.Info(message, zapFields...)
	case zapcore.WarnLevel:
		logger.Warn(message, zapFields...)
	case zapcore.ErrorLevel:
		logger.Error(message, zapFields...)
	case zapcore.FatalLevel:
		logger.Fatal(message, zapFields...)
	}
}

// Error logs an error message with structured fields
func Error(ctx context.Context, message string, fields Fields) {
	logWithLevel(zapcore.ErrorLevel, ctx, message, fields)
}

// Info logs an informational message with structured fields
func Info(ctx context.Context, message string, fields Fields) {
	logWithLevel(zapcore.InfoLevel, ctx, message, fields)
}

// Warn logs a warning message with structured fields
func Warn(ctx context.Context, message string, fields Fields) {
	logWithLevel(zapcore.WarnLevel, ctx, message, fields)
}

// Debug logs a debug message with structured fields
func Debug(ctx context.Context, message string, fields Fields) {
	logWithLevel(zapcore.DebugLevel, ctx, message, fields)
}

// Fatal logs an error message and exits cleanly without stack traces
func Fatal(ctx context.Context, message string, fields Fields) {
	logWithLevel(zapcore.ErrorLevel, ctx, message, fields)
	os.Exit(1)
}
