package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera"

	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
	grpcserver "github.com/LumeraProtocol/supernode/pkg/net/grpc/server"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

type service interface {
	Desc() *grpc.ServiceDesc
}

// Server represents supernode server
type Server struct {
	config       *Config
	services     []service
	name         string
	kr           keyring.Keyring
	grpcServer   *grpcserver.Server
	lumeraClient lumera.Client
	healthServer *health.Server
}

// Run starts the server
func (server *Server) Run(ctx context.Context) error {

	conn.RegisterALTSRecordProtocols()
	defer conn.UnregisterALTSRecordProtocols()

	// Add correlation ID to context
	ctx = logtrace.CtxWithCorrelationID(ctx, server.name)

	// Set up gRPC logging
	logtrace.SetGRPCLogger()
	logtrace.Info(ctx, "Server identity configured", logtrace.Fields{logtrace.FieldModule: "server", "identity": server.config.Identity})
	logtrace.Info(ctx, "Server listening", logtrace.Fields{logtrace.FieldModule: "server", "addresses": server.config.ListenAddresses})

	group, ctx := errgroup.WithContext(ctx)

	addresses := strings.Split(server.config.ListenAddresses, ",")
	if err := server.setupGRPCServer(); err != nil {
		logtrace.Fatal(ctx, "Failed to setup gRPC server", logtrace.Fields{logtrace.FieldModule: "server", logtrace.FieldError: err.Error()})
	}

	// Optimized for streaming 1GB files with 4MB chunks (10 concurrent streams)
	opts := grpcserver.DefaultServerOptions()

	opts.MaxRecvMsgSize = (16 * 1024 * 1024)         // 16MB (supports 4MB chunks + overhead)
	opts.MaxSendMsgSize = (16 * 1024 * 1024)         // 16MB for download streaming
	opts.InitialWindowSize = (16 * 1024 * 1024)      // 16MB per stream (4x chunk size)
	opts.InitialConnWindowSize = (160 * 1024 * 1024) // 160MB (16MB x 10 streams)
	opts.MaxConcurrentStreams = 20                   // Limit to prevent resource exhaustion
	opts.ReadBufferSize = (8 * 1024 * 1024)          // 8MB TCP buffer
	opts.WriteBufferSize = (8 * 1024 * 1024)         // 8MB TCP buffer

	for _, address := range addresses {
		addr := net.JoinHostPort(strings.TrimSpace(address), strconv.Itoa(server.config.Port))
		address := addr // Create a new variable to avoid closure issues

		group.Go(func() error {
			logtrace.Info(ctx, "Starting gRPC server", logtrace.Fields{logtrace.FieldModule: "server", "address": address})
			return server.grpcServer.Serve(ctx, address, opts)
		})
	}

	return group.Wait()
}

func (server *Server) setupGRPCServer() error {
	// Create server credentials
	serverCreds, err := ltc.NewServerCreds(&ltc.ServerOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       server.kr,
			LocalIdentity: server.config.Identity,
			PeerType:      securekeyx.Supernode,
			Validator:     lumera.NewSecureKeyExchangeValidator(server.lumeraClient),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create server credentials: %w", err)
	}

	// Create ltc server
	server.grpcServer = grpcserver.NewServer(server.name, serverCreds)

	// Initialize and register the health server
	server.healthServer = health.NewServer()
	healthpb.RegisterHealthServer(server.grpcServer, server.healthServer)

	// Set all services as serving
	server.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	// Register all services
	var supernodeServer *SupernodeServer
	for _, service := range server.services {
		server.grpcServer.RegisterService(service.Desc(), service)
		server.healthServer.SetServingStatus(service.Desc().ServiceName, healthpb.HealthCheckResponse_SERVING)

		// Keep reference to SupernodeServer
		if ss, ok := service.(*SupernodeServer); ok {
			supernodeServer = ss
		}
	}

	// After all services are registered, update SupernodeServer with the list
	if supernodeServer != nil {
		// Register all custom services
		for _, svc := range server.services {
			supernodeServer.RegisterService(svc.Desc().ServiceName, svc.Desc())
		}

		// Also register the health service
		healthDesc := healthpb.Health_ServiceDesc
		supernodeServer.RegisterService(healthDesc.ServiceName, &healthDesc)
	}

	return nil
}

// SetServiceStatus allows updating the health status of a specific service
func (server *Server) SetServiceStatus(serviceName string, status healthpb.HealthCheckResponse_ServingStatus) {
	if server.healthServer != nil {
		server.healthServer.SetServingStatus(serviceName, status)
	}
}

// Close gracefully stops the server
func (server *Server) Close() {
	if server.healthServer != nil {
		// Set all services to NOT_SERVING before shutdown
		server.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		for _, service := range server.services {
			serviceName := service.Desc().ServiceName
			server.healthServer.SetServingStatus(serviceName, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	}

	// Wrapper handles all gRPC server cleanup
	if server.grpcServer != nil {
		server.grpcServer.Close()
	}
}

// New returns a new Server instance.
func New(config *Config, name string, kr keyring.Keyring, lumeraClient lumera.Client, services ...service) (*Server, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	return &Server{
		config:       config,
		services:     services,
		name:         name,
		kr:           kr,
		lumeraClient: lumeraClient,
	}, nil
}
