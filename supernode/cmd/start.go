package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/cloud"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/sqlite"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	grpcserver "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/server"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/pkg/task"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	statusService "github.com/LumeraProtocol/supernode/v2/supernode/status"
	"github.com/LumeraProtocol/supernode/v2/supernode/transport/gateway"
	cascadeRPC "github.com/LumeraProtocol/supernode/v2/supernode/transport/grpc/cascade"
	server "github.com/LumeraProtocol/supernode/v2/supernode/transport/grpc/status"
	"github.com/LumeraProtocol/supernode/v2/supernode/verifier"

	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/spf13/cobra"

	pbcascade "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"

	pbsupernode "github.com/LumeraProtocol/supernode/v2/gen/supernode"

	// Configure DHT advertised/minimum versions from build-time variables
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the supernode",
	Long: `Start the supernode service using the configuration defined in config.yaml.
The supernode will connect to the Lumera network and begin participating in the network.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Initialize logging
		logtrace.Setup("supernode")

		// Advertise our binary version to peers
		kademlia.SetLocalVersion(Version)
		// Optionally enforce a minimum peer version if provided at build time
		if strings.TrimSpace(MinVer) != "" {
			kademlia.SetMinVersion(MinVer)
		}

		// Create context with correlation ID for tracing
		ctx := logtrace.CtxWithCorrelationID(context.Background(), "supernode-start")
		// Make the context cancelable for graceful shutdown
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Log configuration info
		cfgFile := filepath.Join(baseDir, DefaultConfigFile)
		logtrace.Debug(ctx, "Starting supernode with configuration", logtrace.Fields{"config_file": cfgFile, "keyring_dir": appConfig.GetKeyringDir(), "key_name": appConfig.SupernodeConfig.KeyName})

		// Initialize keyring
		kr, err := initKeyringFromConfig(appConfig)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize keyring", logtrace.Fields{"error": err.Error()})
		}

		// Initialize Lumera client
		lumeraClient, err := initLumeraClient(ctx, appConfig, kr)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to connect Lumera, please check your configuration", logtrace.Fields{"error": err.Error()})
		}

		// Verify config matches chain registration before starting services
		logtrace.Debug(ctx, "Verifying configuration against chain registration", logtrace.Fields{})
		configVerifier := verifier.NewConfigVerifier(appConfig, lumeraClient, kr)
		verificationResult, err := configVerifier.VerifyConfig(ctx)
		if err != nil {
			logtrace.Fatal(ctx, "Config verification failed", logtrace.Fields{"error": err.Error()})
		}

		if !verificationResult.IsValid() {
			logtrace.Fatal(ctx, "Config verification failed", logtrace.Fields{"summary": verificationResult.Summary()})
		}

		if verificationResult.HasWarnings() {
			logtrace.Warn(ctx, "Config verification warnings", logtrace.Fields{"summary": verificationResult.Summary()})
		}

		logtrace.Debug(ctx, "Configuration verification successful", logtrace.Fields{})

		// Set Datadog host to identity and service to latest IP address from chain
		logtrace.SetDatadogHost(appConfig.SupernodeConfig.Identity)
		if snInfo, err := lumeraClient.SuperNode().GetSupernodeWithLatestAddress(ctx, appConfig.SupernodeConfig.Identity); err == nil && snInfo != nil {
			if ip := strings.TrimSpace(snInfo.LatestAddress); ip != "" {
				logtrace.SetDatadogService(ip)
			}
		}

		// Initialize RaptorQ store for Cascade processing
		rqStore, err := initRQStore(ctx, appConfig)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize RaptorQ store", logtrace.Fields{"error": err.Error()})
		}

		// Initialize P2P service
		p2pService, err := initP2PService(ctx, appConfig, lumeraClient, kr, rqStore, nil, nil)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize P2P service", logtrace.Fields{"error": err.Error()})
		}

		// Supernode wrapper removed; components are managed directly

		// Configure cascade service
		cService := cascadeService.NewCascadeService(
			appConfig.SupernodeConfig.Identity,
			lumeraClient,
			p2pService,
			codec.NewRaptorQCodec(appConfig.GetRaptorQFilesDir()),
			rqStore,
		)

		// Create a task tracker and cascade action server with DI
		tr := task.New()
		cascadeActionServer := cascadeRPC.NewCascadeActionServer(cService, tr, 0, 0)

		// Set the version in the status service package
		statusService.Version = Version

		// Create supernode status service with injected tracker
		statusSvc := statusService.NewSupernodeStatusService(p2pService, lumeraClient, appConfig, tr)

		// Create supernode server
		supernodeServer := server.NewSupernodeServer(statusSvc)

		// Create gRPC server (explicit args, no config struct)
		grpcServer, err := server.New(
			appConfig.SupernodeConfig.Identity,
			appConfig.SupernodeConfig.Host,
			int(appConfig.SupernodeConfig.Port),
			"service",
			kr,
			lumeraClient,
			grpcserver.ServiceDesc{Desc: &pbcascade.CascadeService_ServiceDesc, Service: cascadeActionServer},
			grpcserver.ServiceDesc{Desc: &pbsupernode.SupernodeService_ServiceDesc, Service: supernodeServer},
		)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to create gRPC server", logtrace.Fields{"error": err.Error()})
		}

		// Create HTTP gateway server that directly calls the supernode server
		// Pass chain ID for pprof configuration
		gatewayServer, err := gateway.NewServerWithConfig(
			appConfig.SupernodeConfig.Host,
			int(appConfig.SupernodeConfig.GatewayPort),
			supernodeServer,
			appConfig.LumeraClientConfig.ChainID,
		)
		if err != nil {
			return fmt.Errorf("failed to create gateway server: %w", err)
		}

		// Start the services using the standard runner and capture exit
		servicesErr := make(chan error, 1)
		go func() { servicesErr <- RunServices(ctx, grpcServer, cService, p2pService, gatewayServer) }()

		// Set up signal handling for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		// Wait for either a termination signal or service exit
		var triggeredBySignal bool
		var runErr error
		select {
		case sig := <-sigCh:
			triggeredBySignal = true
			logtrace.Debug(ctx, "Received signal, shutting down", logtrace.Fields{"signal": sig.String()})
		case runErr = <-servicesErr:
			if runErr != nil {
				logtrace.Error(ctx, "Service error", logtrace.Fields{"error": runErr.Error()})
			} else {
				logtrace.Debug(ctx, "Services exited", logtrace.Fields{})
			}
		}

		// Cancel context to signal all services
		cancel()

		// Stop HTTP gateway and gRPC servers without blocking shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		go func() {
			if err := gatewayServer.Stop(shutdownCtx); err != nil {
				logtrace.Warn(ctx, "Gateway shutdown warning", logtrace.Fields{"error": err.Error()})
			}
		}()
		grpcServer.Close()

		// Close Lumera client without blocking shutdown
		logtrace.Debug(ctx, "Closing Lumera client", logtrace.Fields{})
		go func() {
			if err := lumeraClient.Close(); err != nil {
				logtrace.Error(ctx, "Error closing Lumera client", logtrace.Fields{"error": err.Error()})
			}
		}()

		// If we triggered shutdown by signal, wait for services to drain
		if triggeredBySignal {
			if err := <-servicesErr; err != nil {
				logtrace.Error(ctx, "Service error on shutdown", logtrace.Fields{"error": err.Error()})
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}

// initP2PService initializes the P2P service
func initP2PService(ctx context.Context, config *config.Config, lumeraClient lumera.Client, kr cKeyring.Keyring, rqStore rqstore.Store, cloud cloud.Storage, mst *sqlite.MigrationMetaStore) (p2p.P2P, error) {
	// Get the supernode address from the keyring
	keyInfo, err := kr.Key(config.SupernodeConfig.KeyName)
	if err != nil {
		return nil, fmt.Errorf("key not found: %w", err)
	}
	address, err := keyInfo.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key: %w", err)
	}

	// Create P2P config using helper function
	p2pConfig := createP2PConfig(config, address.String())

	logtrace.Debug(ctx, "Initializing P2P service", logtrace.Fields{"address": p2pConfig.ListenAddress, "port": p2pConfig.Port, "data_dir": p2pConfig.DataDir, "supernode_id": address.String()})

	p2pService, err := p2p.New(ctx, p2pConfig, lumeraClient, kr, rqStore, cloud, mst)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize p2p service: %w", err)
	}

	return p2pService, nil
}

// initLumeraClient initializes the Lumera client based on configuration
func initLumeraClient(ctx context.Context, config *config.Config, kr cKeyring.Keyring) (lumera.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	lumeraConfig, err := lumera.NewConfig(config.LumeraClientConfig.GRPCAddr, config.LumeraClientConfig.ChainID, config.SupernodeConfig.KeyName, kr)
	if err != nil {
		return nil, fmt.Errorf("failed to create Lumera config: %w", err)
	}
	return lumera.NewClient(
		ctx,
		lumeraConfig,
	)
}

// initRQStore initializes the RaptorQ store for Cascade processing
func initRQStore(ctx context.Context, config *config.Config) (rqstore.Store, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Create RaptorQ store directory if it doesn't exist
	rqDir := config.GetRaptorQFilesDir() + "/rq"
	if err := os.MkdirAll(rqDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create RQ store directory: %w", err)
	}

	// Create the SQLite file path
	rqStoreFile := rqDir + "/rqstore.db"

	logtrace.Debug(ctx, "Initializing RaptorQ store", logtrace.Fields{
		"file_path": rqStoreFile,
	})

	// Initialize RaptorQ store with SQLite
	return rqstore.NewSQLiteRQStore(rqStoreFile)
}
