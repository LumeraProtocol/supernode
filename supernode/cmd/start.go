package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/p2p/kademlia/store/cloud.go"
	"github.com/LumeraProtocol/supernode/p2p/kademlia/store/sqlite"
	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/LumeraProtocol/supernode/supernode/node/action/server/cascade"
	"github.com/LumeraProtocol/supernode/supernode/node/supernode/gateway"
	"github.com/LumeraProtocol/supernode/supernode/node/supernode/server"
	cascadeService "github.com/LumeraProtocol/supernode/supernode/services/cascade"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
	supernodeService "github.com/LumeraProtocol/supernode/supernode/services/common/supernode"

	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/spf13/cobra"
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

		// Create context with correlation ID for tracing
		ctx := logtrace.CtxWithCorrelationID(context.Background(), "supernode-start")

		// Log configuration info
		cfgFile := filepath.Join(baseDir, DefaultConfigFile)
		logtrace.Info(ctx, "Starting supernode with configuration", logtrace.Fields{"config_file": cfgFile, "keyring_dir": appConfig.GetKeyringDir(), "key_name": appConfig.SupernodeConfig.KeyName})

		// Initialize keyring
		kr, err := initKeyringFromConfig(appConfig)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize keyring", logtrace.Fields{"error": err.Error()})
		}

		// Initialize Lumera client
		lumeraClient, err := initLumeraClient(ctx, appConfig, kr)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize Lumera client", logtrace.Fields{"error": err.Error()})
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

		// Initialize the supernode
		supernodeInstance, err := NewSupernode(ctx, appConfig, kr, p2pService, rqStore, lumeraClient)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to initialize supernode", logtrace.Fields{"error": err.Error()})
		}

		// Configure cascade service
		cService := cascadeService.NewCascadeService(
			&cascadeService.Config{
				Config: common.Config{
					SupernodeAccountAddress: appConfig.SupernodeConfig.Identity,
				},
				RqFilesDir: appConfig.GetRaptorQFilesDir(),
			},
			lumeraClient,
			*p2pService,
			codec.NewRaptorQCodec(appConfig.GetRaptorQFilesDir()),
			rqStore,
		)

		// Create cascade action server
		cascadeActionServer := cascade.NewCascadeActionServer(cService)

		// Set the version in the status service package
		supernodeService.Version = Version

		// Create supernode status service
		statusService := supernodeService.NewSupernodeStatusService(*p2pService, lumeraClient, appConfig)
		statusService.RegisterTaskProvider(cService)

		// Create supernode server
		supernodeServer := server.NewSupernodeServer(statusService)

		// Configure server
		serverConfig := &server.Config{
			Identity:        appConfig.SupernodeConfig.Identity,
			ListenAddresses: appConfig.SupernodeConfig.Host,
			Port:            int(appConfig.SupernodeConfig.Port),
		}

		// Create gRPC server
		grpcServer, err := server.New(serverConfig, "service", kr, lumeraClient, cascadeActionServer, supernodeServer)
		if err != nil {
			logtrace.Fatal(ctx, "Failed to create gRPC server", logtrace.Fields{"error": err.Error()})
		}

		// Create HTTP gateway server that directly calls the supernode server
		gatewayPort := appConfig.SupernodeConfig.GatewayPort
		if gatewayPort == 0 {
			gatewayPort = 8002 // Default fallback
		}
		gatewayServer, err := gateway.NewServer(appConfig.SupernodeConfig.Host, int(gatewayPort), supernodeServer)
		if err != nil {
			return fmt.Errorf("failed to create gateway server: %w", err)
		}

		// Start the services
		go func() {
			if err := RunServices(ctx, grpcServer, cService, *p2pService, gatewayServer); err != nil {
				logtrace.Error(ctx, "Service error", logtrace.Fields{"error": err.Error()})
			}
		}()

		// Set up signal handling for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		// Wait for termination signal
		sig := <-sigCh
		logtrace.Info(ctx, "Received signal, shutting down", logtrace.Fields{"signal": sig.String()})

		// Graceful shutdown
		if err := supernodeInstance.Stop(ctx); err != nil {
			logtrace.Error(ctx, "Error during shutdown", logtrace.Fields{"error": err.Error()})
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}

// initP2PService initializes the P2P service
func initP2PService(ctx context.Context, config *config.Config, lumeraClient lumera.Client, kr cKeyring.Keyring, rqStore rqstore.Store, cloud cloud.Storage, mst *sqlite.MigrationMetaStore) (*p2p.P2P, error) {
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

	logtrace.Info(ctx, "Initializing P2P service", logtrace.Fields{"address": p2pConfig.ListenAddress, "port": p2pConfig.Port, "data_dir": p2pConfig.DataDir, "supernode_id": address.String()})

	p2pService, err := p2p.New(ctx, p2pConfig, lumeraClient, kr, rqStore, cloud, mst)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize p2p service: %w", err)
	}

	return &p2pService, nil
}
