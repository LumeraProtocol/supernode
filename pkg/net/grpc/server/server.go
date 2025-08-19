//go:generate mockgen -destination=server_mock.go -package=server -source=server.go

package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

const (
	_      = iota
	KB int = 1 << (10 * iota) // 1024
	MB                        // 1048576
	GB                        // 1073741824
)

const (
	defaultGracefulShutdownTimeout = 30 * time.Second
)

type grpcServer interface {
	GetServiceInfo() map[string]grpc.ServiceInfo
	GracefulStop()
	RegisterService(*grpc.ServiceDesc, interface{})
	Serve(net.Listener) error
	Stop()
}

type ServerOptionBuilder interface {
	buildKeepAliveParams(opts *ServerOptions) keepalive.ServerParameters
	buildKeepAlivePolicy(opts *ServerOptions) keepalive.EnforcementPolicy
}

// Server represents a gRPC server with Lumera ALTS credentials
type Server struct {
	name      string
	creds     credentials.TransportCredentials
	server    *grpc.Server
	services  []ServiceDesc
	listeners []net.Listener
	mu        sync.RWMutex
	done      chan struct{}
	builder   ServerOptionBuilder
}

// ServiceDesc wraps a gRPC service description and its implementation
type ServiceDesc struct {
	Desc    *grpc.ServiceDesc
	Service interface{}
}

// ServerOptions contains options for creating a new server
type ServerOptions struct {
	// Connection parameters
	MaxRecvMsgSize        int   // Maximum message size the server can receive (in bytes)
	MaxSendMsgSize        int   // Maximum message size the server can send (in bytes)
	InitialWindowSize     int32 // Initial window size for stream flow control
	InitialConnWindowSize int32 // Initial window size for connection flow control

	// Server parameters
	MaxConcurrentStreams uint32        // Maximum number of concurrent streams per connection
	GracefulShutdownTime time.Duration // Time to wait for graceful shutdown

	// Keepalive parameters
	MaxConnectionIdle     time.Duration // Maximum time a connection can be idle
	MaxConnectionAge      time.Duration // Maximum time a connection can exist
	MaxConnectionAgeGrace time.Duration // Additional time to wait before forcefully closing
	Time                  time.Duration // Time after which server pings client if there's no activity
	Timeout               time.Duration // Time to wait for ping ack before considering the connection dead
	MinTime               time.Duration // Minimum time client should wait before sending pings
	PermitWithoutStream   bool          // Allow pings even when there are no active streams

	// Additional options
	NumServerWorkers uint32 // Number of server workers (0 means default)
	WriteBufferSize  int    // Size of write buffer
	ReadBufferSize   int    // Size of read buffer
}

// DefaultServerOptions returns default server options
func DefaultServerOptions() *ServerOptions {
	return &ServerOptions{
		MaxRecvMsgSize:        100 * MB,
		MaxSendMsgSize:        100 * MB,
		InitialWindowSize:     (int32)(1 * MB),
		InitialConnWindowSize: (int32)(1 * MB),
		MaxConcurrentStreams:  1000,
		GracefulShutdownTime:  defaultGracefulShutdownTimeout,

		MaxConnectionIdle:     2 * time.Hour,
		MaxConnectionAge:      2 * time.Hour,
		MaxConnectionAgeGrace: 1 * time.Hour,
		Time:                  1 * time.Hour,
		Timeout:               30 * time.Minute,
		MinTime:               5 * time.Minute,
		PermitWithoutStream:   true,

		WriteBufferSize: 32 * KB,
		ReadBufferSize:  32 * KB,
	}
}

// defaultServerOptionBuilder is the default implementation of ServerOptionBuilder
type defaultServerOptionBuilder struct{}

// buildKeepAlivePolicy creates the keepalive enforcement policy
func (b *defaultServerOptionBuilder) buildKeepAliveParams(opts *ServerOptions) keepalive.ServerParameters {
	return keepalive.ServerParameters{
		MaxConnectionIdle:     opts.MaxConnectionIdle,
		MaxConnectionAge:      opts.MaxConnectionAge,
		MaxConnectionAgeGrace: opts.MaxConnectionAgeGrace,
		Time:                  opts.Time,
		Timeout:               opts.Timeout,
	}
}

// buildKeepAlivePolicy creates the keepalive enforcement policy
func (b *defaultServerOptionBuilder) buildKeepAlivePolicy(opts *ServerOptions) keepalive.EnforcementPolicy {
	return keepalive.EnforcementPolicy{
		MinTime:             opts.MinTime,
		PermitWithoutStream: opts.PermitWithoutStream,
	}
}

// NewServer creates a new gRPC server with the given credentials
func NewServer(name string, creds credentials.TransportCredentials) *Server {
	return &Server{
		name:      name,
		creds:     creds,
		services:  make([]ServiceDesc, 0),
		listeners: make([]net.Listener, 0),
		done:      make(chan struct{}),
		builder:   &defaultServerOptionBuilder{},
	}
}

// NewServerWithBuilder creates a new gRPC server with the given credentials and option builder
func NewServerWithBuilder(name string, creds credentials.TransportCredentials, builder ServerOptionBuilder) *Server {
	return &Server{
		name:      name,
		creds:     creds,
		services:  make([]ServiceDesc, 0),
		listeners: make([]net.Listener, 0),
		done:      make(chan struct{}),
		builder:   builder,
	}
}

// buildServerOptions creates all server options including credentials
func (s *Server) buildServerOptions(opts *ServerOptions) []grpc.ServerOption {
	serverOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(opts.MaxRecvMsgSize),
		grpc.MaxSendMsgSize(opts.MaxSendMsgSize),
		grpc.InitialWindowSize(opts.InitialWindowSize),
		grpc.InitialConnWindowSize(opts.InitialConnWindowSize),
		grpc.MaxConcurrentStreams(opts.MaxConcurrentStreams),
		grpc.KeepaliveParams(s.builder.buildKeepAliveParams(opts)),
		grpc.KeepaliveEnforcementPolicy(s.builder.buildKeepAlivePolicy(opts)),
		grpc.WriteBufferSize(opts.WriteBufferSize),
		grpc.ReadBufferSize(opts.ReadBufferSize),
	}

	if opts.NumServerWorkers > 0 {
		serverOpts = append(serverOpts, grpc.NumStreamWorkers(opts.NumServerWorkers))
	}

	// Add credentials based on environment
	if os.Getenv("INTEGRATION_TEST_ENV") == "true" {
		serverOpts = append(serverOpts, grpc.Creds(insecure.NewCredentials()))
	} else {
		serverOpts = append(serverOpts, grpc.Creds(s.creds))
	}

	return serverOpts
}

// RegisterService registers a gRPC service with the server
func (s *Server) RegisterService(desc *grpc.ServiceDesc, impl interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.services = append(s.services, ServiceDesc{
		Desc:    desc,
		Service: impl,
	})
}

// createListener creates a TCP listener for the given address
func (s *Server) createListener(ctx context.Context, address string) (net.Listener, error) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.Errorf("failed to create listener: %w", err).WithField("address", address)
	}
	logtrace.Info(ctx, "gRPC server listening", logtrace.Fields{"address": address})
	return lis, nil
}

// Serve starts the gRPC server on the given address
func (s *Server) Serve(ctx context.Context, address string, opts *ServerOptions) error {
	if ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}

	if opts == nil {
		opts = DefaultServerOptions()
	}

	logtrace.SetGRPCLogger()
	ctx = logtrace.CtxWithCorrelationID(ctx, s.name)

	// Create server with options
	serverOpts := s.buildServerOptions(opts)
	s.server = grpc.NewServer(serverOpts...)

	// Register services
	s.mu.RLock()
	for _, service := range s.services {
		s.server.RegisterService(service.Desc, service.Service)
	}
	s.mu.RUnlock()

	// Enable reflection
	reflection.Register(s.server)

	// Create listener
	lis, err := s.createListener(ctx, address)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.listeners = append(s.listeners, lis)
	s.mu.Unlock()

	// Start serving in a goroutine
	serveErr := make(chan error, 1)
	go func() {
		if err := s.server.Serve(lis); err != nil {
			serveErr <- errors.Errorf("serve: %w", err).WithField("address", address)
		}
		close(serveErr)
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		logtrace.Info(ctx, "Shutting down gRPC server", logtrace.Fields{"address": address})
		return s.Stop(opts.GracefulShutdownTime)
	case err := <-serveErr:
		return err
	}
}

// Stop gracefully stops the server with a timeout
func (s *Server) Stop(timeout time.Duration) error {
	if s.server == nil {
		return nil
	}

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create channel to signal completion of graceful stop
	stopped := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or timeout
	select {
	case <-ctx.Done():
		s.server.Stop()
		return fmt.Errorf("server shutdown timed out")
	case <-stopped:
		return nil
	}
}

// Close immediately stops the server and closes all listeners
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		s.server.Stop()
		s.server = nil
	}

	// Close all listeners
	var errs []error
	for _, lis := range s.listeners {
		if err := lis.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	s.listeners = nil

	if len(errs) > 0 {
		return fmt.Errorf("errors closing listeners: %v", errs)
	}
	return nil
}
