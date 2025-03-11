package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Connection defines the interface for a client connection
type Connection interface {
	Close() error
	GetConn() *grpc.ClientConn
}

// grpcConnection wraps a gRPC connection
type grpcConnection struct {
	conn *grpc.ClientConn
}

// newGRPCConnection creates a new gRPC connection
func newGRPCConnection(ctx context.Context, addr string) (Connection, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Note: Cosmos SDK doesn't support TLS for gRPC so we use insecure credentials
	conn, err := grpc.DialContext(
		dialCtx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &grpcConnection{
		conn: conn,
	}, nil
}

// Close closes the gRPC connection
func (c *grpcConnection) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// GetConn returns the underlying gRPC connection
func (c *grpcConnection) GetConn() *grpc.ClientConn {
	return c.conn
}
