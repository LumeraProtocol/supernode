package logtrace

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/grpc/grpclog"
)

// grpcLogger implements grpclog.LoggerV2 interface using logtrace
type grpcLogger struct{}

// NewGRPCLogger creates a new gRPC-compatible logger using logtrace
func NewGRPCLogger() grpclog.LoggerV2 {
	return &grpcLogger{}
}

// Info logs at info level
func (g *grpcLogger) Info(args ...any) {
	Debug(context.Background(), fmt.Sprint(args...), Fields{"module": "grpc"}) // Suppress Internal Logs
}

// Infof logs at info level with format
func (g *grpcLogger) Infof(format string, args ...any) {
	Debug(context.Background(), fmt.Sprintf(format, args...), Fields{"module": "grpc"})
}

// Infoln logs at info level with newline
func (g *grpcLogger) Infoln(args ...any) {
	g.Info(args...)
}

// Warning logs at warn level
func (g *grpcLogger) Warning(args ...any) {
	Warn(context.Background(), fmt.Sprint(args...), Fields{"module": "grpc"})
}

// Warningf logs at warn level with format
func (g *grpcLogger) Warningf(format string, args ...any) {
	Warn(context.Background(), fmt.Sprintf(format, args...), Fields{"module": "grpc"})
}

// Warningln logs at warn level with newline
func (g *grpcLogger) Warningln(args ...any) {
	g.Warning(args...)
}

// Error logs at error level
func (g *grpcLogger) Error(args ...any) {
	Error(context.Background(), fmt.Sprint(args...), Fields{"module": "grpc"})
}

// Errorf logs at error level with format
func (g *grpcLogger) Errorf(format string, args ...any) {
	Error(context.Background(), fmt.Sprintf(format, args...), Fields{"module": "grpc"})
}

// Errorln logs at error level with newline
func (g *grpcLogger) Errorln(args ...any) {
	g.Error(args...)
}

// Fatal logs at error level and exits gracefully
func (g *grpcLogger) Fatal(args ...any) {
	msg := fmt.Sprint(args...)
	Error(context.Background(), msg, Fields{"module": "grpc", "level": "fatal"})
	os.Exit(1)
}

// Fatalf logs at error level with format and exits gracefully
func (g *grpcLogger) Fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	Error(context.Background(), msg, Fields{"module": "grpc", "level": "fatal"})
	os.Exit(1)
}

// Fatalln logs at error level with newline and exits
func (g *grpcLogger) Fatalln(args ...any) {
	g.Fatal(args...)
}

// V returns true if logging is enabled for the given verbosity level
func (g *grpcLogger) V(l int) bool {
	return l <= 1 // Only show error/warning levels by default
}

// SetGRPCLogger configures gRPC to use logtrace for internal logging
func SetGRPCLogger() {
	grpclog.SetLoggerV2(NewGRPCLogger())
}
