package logtrace

import (
	"context"
	"fmt"

	"google.golang.org/grpc/grpclog"
)

// grpcLogger implements grpclog.LoggerV2 interface using logtrace
type grpcLogger struct {
	ctx context.Context
}

// NewGRPCLogger creates a new gRPC-compatible logger using logtrace
func NewGRPCLogger(ctx context.Context) grpclog.LoggerV2 {
	return &grpcLogger{ctx: ctx}
}

// Info logs at info level
func (g *grpcLogger) Info(args ...interface{}) {
	Info(g.ctx, fmt.Sprint(args...), Fields{FieldModule: "grpc"})
}

// Infof logs at info level with format
func (g *grpcLogger) Infof(format string, args ...interface{}) {
	Info(g.ctx, fmt.Sprintf(format, args...), Fields{FieldModule: "grpc"})
}

// Infoln logs at info level with newline
func (g *grpcLogger) Infoln(args ...interface{}) {
	g.Info(args...)
}

// Warning logs at warn level
func (g *grpcLogger) Warning(args ...interface{}) {
	Warn(g.ctx, fmt.Sprint(args...), Fields{FieldModule: "grpc"})
}

// Warningf logs at warn level with format
func (g *grpcLogger) Warningf(format string, args ...interface{}) {
	Warn(g.ctx, fmt.Sprintf(format, args...), Fields{FieldModule: "grpc"})
}

// Warningln logs at warn level with newline
func (g *grpcLogger) Warningln(args ...interface{}) {
	g.Warning(args...)
}

// Error logs at error level
func (g *grpcLogger) Error(args ...interface{}) {
	Error(g.ctx, fmt.Sprint(args...), Fields{FieldModule: "grpc"})
}

// Errorf logs at error level with format
func (g *grpcLogger) Errorf(format string, args ...interface{}) {
	Error(g.ctx, fmt.Sprintf(format, args...), Fields{FieldModule: "grpc"})
}

// Errorln logs at error level with newline
func (g *grpcLogger) Errorln(args ...interface{}) {
	g.Error(args...)
}

// Fatal logs at error level and panics
func (g *grpcLogger) Fatal(args ...interface{}) {
	msg := fmt.Sprint(args...)
	Error(g.ctx, msg, Fields{FieldModule: "grpc", "level": "fatal"})
	panic(msg)
}

// Fatalf logs at error level with format and panics
func (g *grpcLogger) Fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	Error(g.ctx, msg, Fields{FieldModule: "grpc", "level": "fatal"})
	panic(msg)
}

// Fatalln logs at error level with newline and panics
func (g *grpcLogger) Fatalln(args ...interface{}) {
	g.Fatal(args...)
}

// V returns true if logging is enabled for the given verbosity level
func (g *grpcLogger) V(l int) bool {
	return l <= 1 // Only show error/warning levels by default
}

// SetGRPCLogger configures gRPC to use logtrace for internal logging
func SetGRPCLogger(ctx context.Context) {
	grpclog.SetLoggerV2(NewGRPCLogger(ctx))
}
