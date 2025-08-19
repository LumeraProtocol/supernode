package net

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// UnaryServerInterceptor injects a correlation ID into the context for gRPC requests
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		var correlationID string
		if ok {
			if values := md.Get("x-correlation-id"); len(values) > 0 {
				correlationID = values[0]
			}
		}
		if correlationID == "" {
			correlationID = uuid.NewString()
		}
		ctx = logtrace.CtxWithCorrelationID(ctx, correlationID)

		fields := logtrace.Fields{
			logtrace.FieldMethod:        info.FullMethod,
			logtrace.FieldCorrelationID: correlationID,
		}
		logtrace.Info(ctx, "received gRPC request", fields)

		resp, err := handler(ctx, req)

		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "gRPC request failed", fields)
		} else {
			logtrace.Info(ctx, "gRPC request processed successfully", fields)
		}

		return resp, err
	}
}

func AddCorrelationID(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		if values := md.Get("x-correlation-id"); len(values) > 0 {
			return ctx
		}
	}

	correlationID := uuid.NewString()
	md = metadata.Pairs("x-correlation-id", correlationID)
	return metadata.NewOutgoingContext(ctx, md)
}
