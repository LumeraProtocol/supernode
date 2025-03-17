// File: cmd/supernode.go
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
		logtrace.Error(ctx, "Config is nil", logtrace.Fields{})
		return nil, fmt.Errorf("config is nil")
	}

	// Initialize Lumera client
	logtrace.Info(ctx, "Initializing Lumera client", logtrace.Fields{
		"grpc_addr": config.LumeraClientConfig.GRPCAddr,
		"chain_id":  config.LumeraClientConfig.ChainID,
		"timeout":   config.LumeraClientConfig.Timeout,
	})

	lumeraClient, err := initLumeraClient(ctx, config)
	if err != nil {
		logtrace.Error(ctx, "Failed to initialize Lumera client", logtrace.Fields{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to initialize Lumera client: %w", err)
	}

	// Initialize RaptorQ store for Cascade processing
	logtrace.Info(ctx, "Initializing RaptorQ store", logtrace.Fields{
		"data_dir": config.P2PConfig.DataDir,
	})

	rqStore, err := initRQStore(ctx, config)
	if err != nil {
		logtrace.Error(ctx, "Failed to initialize RaptorQ store", logtrace.Fields{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to initialize RaptorQ store: %w", err)
	}

	// Create the supernode instance
	logtrace.Info(ctx, "Creating supernode instance", logtrace.Fields{
		"key_name": config.SupernodeConfig.KeyName,
	})

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
	// Verify that the key specified in config exists
	logtrace.Info(ctx, "Verifying key exists in keyring", logtrace.Fields{
		"key_name": s.keyName,
	})

	keyInfo, err := s.keyring.Key(s.keyName)
	if err != nil {
		logtrace.Error(ctx, "Key not found in keyring", logtrace.Fields{
			"key_name": s.keyName,
			"error":    err.Error(),
		})

		// Provide helpful guidance
		fmt.Printf("\nError: Key '%s' not found in keyring at %s\n",
			s.keyName, s.config.KeyringConfig.Dir)
		fmt.Println("\nPlease create the key first with one of these commands:")
		fmt.Printf("  supernode keys add %s\n", s.keyName)
		fmt.Printf("  supernode keys recover %s\n", s.keyName)
		return fmt.Errorf("key not found")
	}

	// Get the account address for logging
	address, err := keyInfo.GetAddress()
	if err != nil {
		logtrace.Error(ctx, "Failed to get address from key", logtrace.Fields{
			"key_name": s.keyName,
			"error":    err.Error(),
		})
		return err
	}

	// Store account address for future use
	s.accountAddress = address.String()

	logtrace.Info(ctx, "Found valid key in keyring", logtrace.Fields{
		"key_name": s.keyName,
		"address":  s.accountAddress,
	})

	// Configure P2P service
	p2pConfig := &p2p.Config{
		ListenAddress:  s.config.P2PConfig.ListenAddress,
		Port:           s.config.P2PConfig.Port,
		DataDir:        s.config.P2PConfig.DataDir,
		BootstrapNodes: s.config.P2PConfig.BootstrapNodes,
		ExternalIP:     s.config.P2PConfig.ExternalIP,
		ID:             s.accountAddress,
	}

	logtrace.Info(ctx, "Initializing P2P service", logtrace.Fields{
		"listen_address": p2pConfig.ListenAddress,
		"port":           p2pConfig.Port,
		"data_dir":       p2pConfig.DataDir,
		"supernode_id":   p2pConfig.ID,
		"bootstrap":      p2pConfig.BootstrapNodes,
	})

	p2pService, err := p2p.New(ctx, p2pConfig, s.lumeraClient, s.keyring, s.rqStore, nil, nil)
	if err != nil {
		logtrace.Error(ctx, "Failed to initialize P2P service", logtrace.Fields{
			"error": err.Error(),
		})
		return fmt.Errorf("failed to initialize p2p service: %w", err)
	}
	s.p2pService = p2pService

	// Run the p2p service
	logtrace.Info(ctx, "Starting P2P service", logtrace.Fields{})
	if err := s.p2pService.Run(ctx); err != nil {
		logtrace.Error(ctx, "P2P service error", logtrace.Fields{
			"error": err.Error(),
		})
		return fmt.Errorf("p2p service error: %w", err)
	}

	logtrace.Info(ctx, "Supernode successfully started", logtrace.Fields{
		"address": s.accountAddress,
	})

	return nil
}

// Stop stops all supernode services
func (s *Supernode) Stop(ctx context.Context) error {
	logtrace.Info(ctx, "Stopping supernode services", logtrace.Fields{})

	// Stop P2P service if it exists
	if s.p2pService != nil {
		logtrace.Info(ctx, "Stopping P2P service", logtrace.Fields{})
		// Note: The actual p2p.Stop() method might need to be implemented
	}

	// Close the Lumera client connection
	if s.lumeraClient != nil {
		logtrace.Info(ctx, "Closing Lumera client", logtrace.Fields{})
		if err := s.lumeraClient.Close(); err != nil {
			logtrace.Error(ctx, "Error closing Lumera client", logtrace.Fields{
				"error": err.Error(),
			})
		}
	}

	// Close RQ store if needed
	if s.rqStore != nil {
		logtrace.Info(ctx, "Closing RaptorQ store", logtrace.Fields{})
		// Note: The actual rqstore.Close() method might need to be implemented
	}

	logtrace.Info(ctx, "Supernode stopped successfully", logtrace.Fields{})
	return nil
}

// initLumeraClient initializes the Lumera client based on configuration
func initLumeraClient(ctx context.Context, config *config.Config) (lumera.Client, error) {
	if config == nil {
		logtrace.Error(ctx, "Config is nil during Lumera client initialization", logtrace.Fields{})
		return nil, fmt.Errorf("config is nil")
	}

	logtrace.Info(ctx, "Creating Lumera client", logtrace.Fields{
		"grpc_addr": config.LumeraClientConfig.GRPCAddr,
		"chain_id":  config.LumeraClientConfig.ChainID,
		"timeout":   config.LumeraClientConfig.Timeout,
	})

	client, err := lumera.NewClient(
		ctx,
		lumera.WithGRPCAddr(config.LumeraClientConfig.GRPCAddr),
		lumera.WithChainID(config.LumeraClientConfig.ChainID),
		lumera.WithTimeout(config.LumeraClientConfig.Timeout),
	)

	if err != nil {
		logtrace.Error(ctx, "Failed to create Lumera client", logtrace.Fields{
			"error": err.Error(),
		})
		return nil, err
	}

	logtrace.Info(ctx, "Lumera client created successfully", logtrace.Fields{})
	return client, nil
}

// initRQStore initializes the RaptorQ store for Cascade processing
func initRQStore(ctx context.Context, config *config.Config) (rqstore.Store, error) {
	if config == nil {
		logtrace.Error(ctx, "Config is nil during RQ store initialization", logtrace.Fields{})
		return nil, fmt.Errorf("config is nil")
	}

	// Create RaptorQ store directory if it doesn't exist
	rqDir := config.P2PConfig.DataDir + "/rq"
	logtrace.Info(ctx, "Creating RaptorQ store directory", logtrace.Fields{
		"directory": rqDir,
	})

	if err := os.MkdirAll(rqDir, 0700); err != nil {
		logtrace.Error(ctx, "Failed to create RQ store directory", logtrace.Fields{
			"directory": rqDir,
			"error":     err.Error(),
		})
		return nil, fmt.Errorf("failed to create RQ store directory: %w", err)
	}

	// Create the SQLite file path
	rqStoreFile := rqDir + "/rqstore.db"

	logtrace.Info(ctx, "Initializing SQLite RaptorQ store", logtrace.Fields{
		"file_path": rqStoreFile,
	})

	// Initialize RaptorQ store with SQLite
	store, err := rqstore.NewSQLiteRQStore(rqStoreFile)
	if err != nil {
		logtrace.Error(ctx, "Failed to create SQLite RQ store", logtrace.Fields{
			"file_path": rqStoreFile,
			"error":     err.Error(),
		})
		return nil, err
	}

	logtrace.Info(ctx, "RaptorQ store initialized successfully", logtrace.Fields{})
	return store, nil
}
