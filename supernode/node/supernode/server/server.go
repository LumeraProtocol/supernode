package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/log"
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
	grpclog.SetLoggerV2(log.NewLoggerWithErrorLevel())
	log.WithContext(ctx).Infof("Server identity: %s", server.config.Identity)
	log.WithContext(ctx).Infof("Listening on: %s", server.config.ListenAddresses)
	ctx = log.ContextWithPrefix(ctx, server.name)

	group, ctx := errgroup.WithContext(ctx)

	addresses := strings.Split(server.config.ListenAddresses, ",")
	if err := server.setupGRPCServer(); err != nil {
		return fmt.Errorf("failed to setup gRPC server: %w", err)
	}

	// Custom server options
	opts := grpcserver.DefaultServerOptions()

	opts.GracefulShutdownTime = 60 * time.Second
	opts.MaxConnectionIdle = 1 * time.Hour
	opts.MaxConnectionAge = 1 * time.Hour

	// Frequent keepalive to detect issues early
	opts.Time = 5 * time.Minute    // Ping every 5 mins
	opts.Timeout = 2 * time.Minute // 2 min ping timeout

	// CRITICAL: Large flow control windows for 1GB files
	opts.InitialWindowSize = (int32)(16 * 1024 * 1024)     // 16MB (was 1MB)
	opts.InitialConnWindowSize = (int32)(16 * 1024 * 1024) // 16MB (was 1MB)

	// Large message and buffer sizes for 1GB streaming
	opts.MaxRecvMsgSize = 500 * 1024 * 1024 // 500MB
	opts.MaxSendMsgSize = 500 * 1024 * 1024 // 500MB
	opts.WriteBufferSize = 256 * 1024       // 256KB buffer
	opts.ReadBufferSize = 256 * 1024        // 256KB buffer

	for _, address := range addresses {
		addr := net.JoinHostPort(strings.TrimSpace(address), strconv.Itoa(server.config.Port))
		address := addr // Create a new variable to avoid closure issues

		group.Go(func() error {
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

	// Create wrapper server
	server.grpcServer = grpcserver.NewServer(server.name, serverCreds)

	// Setup health server
	server.healthServer = health.NewServer()
	server.grpcServer.RegisterService(&healthpb.Health_ServiceDesc, server.healthServer)
	server.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	// Register all services
	for _, service := range server.services {
		server.grpcServer.RegisterService(service.Desc(), service)
		server.healthServer.SetServingStatus(service.Desc().ServiceName, healthpb.HealthCheckResponse_SERVING)
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
