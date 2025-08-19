package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Supernode represents a supernode in the Lumera network
type Supernode struct {
	config       *config.Config
	lumeraClient lumera.Client
	p2pService   p2p.P2P
	keyring      keyring.Keyring
	rqStore      rqstore.Store
	keyName      string // String that represents the supernode account in keyring
}

// NewSupernode creates a new supernode instance
func NewSupernode(ctx context.Context, config *config.Config, kr keyring.Keyring,
	p2pClient *p2p.P2P, rqStore rqstore.Store, lumeraClient lumera.Client) (*Supernode, error) {

	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	supernode := &Supernode{
		config:       config,
		lumeraClient: lumeraClient,
		keyring:      kr,
		rqStore:      rqStore,
		p2pService:   *p2pClient,
		keyName:      config.SupernodeConfig.KeyName,
	}

	return supernode, nil
}

// Start starts all supernode services
func (s *Supernode) Start(ctx context.Context) error {
	// Verify that the key specified in config exists
	keyInfo, err := s.keyring.Key(s.config.SupernodeConfig.KeyName)
	if err != nil {
		logtrace.Error(ctx, "Key not found in keyring", logtrace.Fields{
			"key_name": s.config.SupernodeConfig.KeyName,
			"error":    err.Error(),
		})

		// Provide helpful guidance
		fmt.Printf("\nError: Key '%s' not found in keyring at %s\n",
			s.config.SupernodeConfig.KeyName, s.config.GetKeyringDir())
		fmt.Println("\nPlease create the key first with one of these commands:")
		fmt.Printf("  supernode keys add %s\n", s.config.SupernodeConfig.KeyName)
		fmt.Printf("  supernode keys recover %s\n", s.config.SupernodeConfig.KeyName)
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
		"key_name": s.config.SupernodeConfig.KeyName,
		"address":  address.String(),
	})

	// Use the P2P service that was passed in via constructor
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
func initLumeraClient(ctx context.Context, config *config.Config, kr keyring.Keyring) (lumera.Client, error) {
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

	logtrace.Info(ctx, "Initializing RaptorQ store", logtrace.Fields{
		"file_path": rqStoreFile,
	})

	// Initialize RaptorQ store with SQLite
	return rqstore.NewSQLiteRQStore(rqStoreFile)
}
