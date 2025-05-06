package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"

	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
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
	grpcServer   *grpc.Server
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

	for _, address := range addresses {
		addr := net.JoinHostPort(strings.TrimSpace(address), strconv.Itoa(server.config.Port))
		address := addr // Create a new variable to avoid closure issues

		group.Go(func() error {
			return server.listen(ctx, address)
		})
	}

	return group.Wait()
}

func (server *Server) listen(ctx context.Context, address string) (err error) {
	listen, err := net.Listen("tcp", address)
	if err != nil {
		return errors.Errorf("listen: %w", err).WithField("address", address)
	}

	errCh := make(chan error, 1)
	go func() {
		defer errors.Recover(func(recErr error) { err = recErr })
		log.WithContext(ctx).Infof("gRPC server listening securely on %q", address)
		if err := server.grpcServer.Serve(listen); err != nil {
			errCh <- errors.Errorf("serve: %w", err).WithField("address", address)
		}
	}()

	select {
	case <-ctx.Done():
		log.WithContext(ctx).Infof("Shutting down gRPC server at %q", address)
		server.grpcServer.GracefulStop()
	case err := <-errCh:
		return err
	}

	return nil
}

func (server *Server) setupGRPCServer() error {
	// Create server credentials
	serverCreds, err := ltc.NewServerCreds(&ltc.ServerOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       server.kr,
			LocalIdentity: server.config.Identity,
			PeerType:      securekeyx.Supernode,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create server credentials: %w", err)
	}

	// Initialize the gRPC server with credentials (secure)
	server.grpcServer = grpc.NewServer(grpc.Creds(serverCreds))

	// Initialize and register the health server
	server.healthServer = health.NewServer()
	healthpb.RegisterHealthServer(server.grpcServer, server.healthServer)

	// Register reflection service
	reflection.Register(server.grpcServer)

	// Set all services as serving
	server.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	// Register all services and set their health status
	for _, service := range server.services {
		serviceName := service.Desc().ServiceName
		server.grpcServer.RegisterService(service.Desc(), service)
		server.healthServer.SetServingStatus(serviceName, healthpb.HealthCheckResponse_SERVING)
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

		// Allow a short time for health status to propagate
		for _, service := range server.services {
			serviceName := service.Desc().ServiceName
			server.healthServer.SetServingStatus(serviceName, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	}

	if server.grpcServer != nil {
		server.grpcServer.GracefulStop()
	}
}

// New returns a new Server instance.
func New(config *Config, name string, kr keyring.Keyring, services ...service) (*Server, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	return &Server{
		config:   config,
		services: services,
		name:     name,
		kr:       kr,
	}, nil
}
