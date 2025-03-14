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
	config       *config.Config
	lumeraClient lumera.Client
	p2pService   p2p.P2P
	keyring      keyring.Keyring
	rqStore      rqstore.Store
}

// NewSupernode creates a new supernode instance
func NewSupernode(ctx context.Context, config *config.Config) (*Supernode, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Initialize keyring
	kr, err := initKeyring(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize keyring: %w", err)
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
	}

	return supernode, nil
}

// Start starts all supernode services
func (s *Supernode) Start(ctx context.Context) error {
	// Initialize p2p service
	p2pConfig := &p2p.Config{
		ListenAddress:  s.config.P2P.ListenAddress,
		Port:           s.config.P2P.Port,
		DataDir:        s.config.P2P.DataDir,
		BootstrapNodes: s.config.P2P.BootstrapNodes,
		ExternalIP:     s.config.P2P.ExternalIP,
		ID:             s.config.SupernodeID,
	}

	logtrace.Info(ctx, "Initializing P2P service", logtrace.Fields{
		"listen_address": p2pConfig.ListenAddress,
		"port":           p2pConfig.Port,
		"data_dir":       p2pConfig.DataDir,
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

// initKeyring initializes the keyring based on configuration
func initKeyring(ctx context.Context, config *config.Config) (keyring.Keyring, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Set default directory if not provided
	keyringDir := "./keys"
	if config.Keyring.Dir != "" {
		keyringDir = config.Keyring.Dir
	}

	// Set default backend if not provided
	backend := keyring.BackendFile
	if config.Keyring.Backend != "" {
		switch config.Keyring.Backend {
		case "file":
			backend = keyring.BackendFile
		case "os":
			backend = keyring.BackendOS
		case "memory":
			backend = keyring.BackendMemory
		default:
			logtrace.Warn(ctx, "Unsupported keyring backend, using file backend", logtrace.Fields{
				"backend": config.Keyring.Backend,
			})
		}
	}

	// Create the keyring directory if it doesn't exist
	if err := os.MkdirAll(keyringDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create keyring directory: %w", err)
	}

	logtrace.Info(ctx, "Initializing keyring", logtrace.Fields{
		"backend":   backend,
		"directory": keyringDir,
	})

	// Initialize the keyring
	kr, err := keyring.New(
		"lumera",
		backend,
		keyringDir,
		os.Stdin,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize keyring: %w", err)
	}

	return kr, nil
}

// initLumeraClient initializes the Lumera client based on configuration
func initLumeraClient(ctx context.Context, config *config.Config) (lumera.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Set default values if not provided
	grpcAddr := "localhost:9090"
	if config.Lumera.GRPCAddr != "" {
		grpcAddr = config.Lumera.GRPCAddr
	}

	chainID := "lumera"
	if config.Lumera.ChainID != "" {
		chainID = config.Lumera.ChainID
	}

	timeout := 10
	if config.Lumera.Timeout > 0 {
		timeout = config.Lumera.Timeout
	}

	logtrace.Info(ctx, "Initializing Lumera client", logtrace.Fields{
		"grpc_addr": grpcAddr,
		"chain_id":  chainID,
		"timeout":   timeout,
	})

	return lumera.NewClient(
		ctx,
		lumera.WithGRPCAddr(grpcAddr),
		lumera.WithChainID(chainID),
		lumera.WithTimeout(timeout),
	)
}

// initRQStore initializes the RaptorQ store for Cascade processing
func initRQStore(ctx context.Context, config *config.Config) (rqstore.Store, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Set default directory if not provided
	dataDir := "./data/p2p"
	if config.P2P.DataDir != "" {
		dataDir = config.P2P.DataDir
	}

	// Create RaptorQ store directory if it doesn't exist
	rqDir := dataDir + "/rq"
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
