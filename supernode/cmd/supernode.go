package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Supernode represents a supernode in the Lumera network
type Supernode struct {
	config         *config.Config
	lumeraClient   lumera.Client
	p2pService     p2p.P2P
	keyring        keyring.Keyring
	rqStore        rqstore.Store
	keyName        string // String that represents the supernode account in keyring
	accountAddress string // String that represents the supernode account address lemera12Xxxxx
}

// NewSupernode creates a new supernode instance
func NewSupernode(ctx context.Context, config *config.Config, kr keyring.Keyring) (*Supernode, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Initialize Lumera client
	lumeraClient, err := initLumeraClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Lumera client: %w", err)
	}

	// Initialize RaptorQ store for Cascade processing
	rqStore, err := initRQStore(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize RaptorQ store: %w", err)
	}

	// Create the supernode instance
	supernode := &Supernode{
		config:       config,
		lumeraClient: lumeraClient,
		keyring:      kr,
		rqStore:      rqStore,
		keyName:      config.SupernodeConfig.KeyName,
	}

	return supernode, nil
}

// Start starts all supernode services
func (s *Supernode) Start(ctx context.Context) error {
	// Initialize p2p service

	// Verify that the key specified in config exists
	keyInfo, err := s.keyring.Key(appConfig.SupernodeConfig.KeyName)
	if err != nil {
		logtrace.Error(ctx, "Key not found in keyring", logtrace.Fields{
			"key_name": appConfig.SupernodeConfig.KeyName,
			"error":    err.Error(),
		})

		// Provide helpful guidance
		fmt.Printf("\nError: Key '%s' not found in keyring at %s\n",
			appConfig.SupernodeConfig.KeyName, appConfig.KeyringConfig.Dir)
		fmt.Println("\nPlease create the key first with one of these commands:")
		fmt.Printf("  supernode keys add %s\n", appConfig.SupernodeConfig.KeyName)
		fmt.Printf("  supernode keys recover %s\n", appConfig.SupernodeConfig.KeyName)
		return fmt.Errorf("key not found")
	}

	// Get the account address for logging
	address, err := keyInfo.GetAddress()
	if err != nil {
		logtrace.Error(ctx, "Failed to get address from key", logtrace.Fields{
			"error": err.Error(),
		})
		return err
	}

	logtrace.Info(ctx, "Found valid key in keyring", logtrace.Fields{
		"key_name": appConfig.SupernodeConfig.KeyName,
		"address":  address.String(),
	})

	p2pConfig := &p2p.Config{
		ListenAddress:  s.config.P2PConfig.ListenAddress,
		Port:           s.config.P2PConfig.Port,
		DataDir:        s.config.P2PConfig.DataDir,
		BootstrapNodes: s.config.P2PConfig.BootstrapNodes,
		ExternalIP:     s.config.P2PConfig.ExternalIP,
		ID:             address.String(),
	}

	logtrace.Info(ctx, "Initializing P2P service", logtrace.Fields{
		"listen_address": p2pConfig.ListenAddress,
		"port":           p2pConfig.Port,
		"data_dir":       p2pConfig.DataDir,
		"supernode_id":   address.String(),
	})

	p2pService, err := p2p.New(ctx, p2pConfig, s.lumeraClient, s.keyring, s.rqStore, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize p2p service: %w", err)
	}
	s.p2pService = p2pService

	// Run the p2p service
	logtrace.Info(ctx, "Starting P2P service", logtrace.Fields{})
	if err := s.p2pService.Run(ctx); err != nil {
		return fmt.Errorf("p2p service error: %w", err)
	}

	return nil
}

// Stop stops all supernode services
func (s *Supernode) Stop(ctx context.Context) error {
	// Close the Lumera client connection
	if s.lumeraClient != nil {
		logtrace.Info(ctx, "Closing Lumera client", logtrace.Fields{})
		if err := s.lumeraClient.Close(); err != nil {
			logtrace.Error(ctx, "Error closing Lumera client", logtrace.Fields{
				"error": err.Error(),
			})
		}
	}

	return nil
}

// initLumeraClient initializes the Lumera client based on configuration
func initLumeraClient(ctx context.Context, config *config.Config) (lumera.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	logtrace.Info(ctx, "Initializing Lumera client", logtrace.Fields{
		"grpc_addr": config.LumeraClientConfig.GRPCAddr,
		"chain_id":  config.LumeraClientConfig.ChainID,
		"timeout":   config.LumeraClientConfig.Timeout,
	})

	return lumera.NewClient(
		ctx,
		lumera.WithGRPCAddr(config.LumeraClientConfig.GRPCAddr),
		lumera.WithChainID(config.LumeraClientConfig.ChainID),
		lumera.WithTimeout(config.LumeraClientConfig.Timeout),
	)
}

// initRQStore initializes the RaptorQ store for Cascade processing
func initRQStore(ctx context.Context, config *config.Config) (rqstore.Store, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Create RaptorQ store directory if it doesn't exist
	rqDir := config.P2PConfig.DataDir + "/rq"
	if err := os.MkdirAll(rqDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create RQ store directory: %w", err)
	}

	// Create the SQLite file path
	rqStoreFile := rqDir + "/rqstore.db"

	logtrace.Info(ctx, "Initializing RaptorQ store", logtrace.Fields{
		"file_path": rqStoreFile,
	})

	// Initialize RaptorQ store with SQLite
	return rqstore.NewSQLiteRQStore(rqStoreFile)
}
