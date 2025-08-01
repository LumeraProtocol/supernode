package cli

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/BurntSushi/toml"
	"github.com/spf13/pflag"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	snkeyring "github.com/LumeraProtocol/supernode/pkg/keyring"
	sdkcfg "github.com/LumeraProtocol/supernode/sdk/config"
	sdklog "github.com/LumeraProtocol/supernode/sdk/log"
	sdknet "github.com/LumeraProtocol/supernode/sdk/net"
)

const (
	defaultConfigFileName = "config.toml"
)

type CLI struct {
	opts CLIOptions
	cfg  *CLIConfig
	kr   keyring.Keyring
	sdkConfig sdkcfg.Config
	lumeraClient lumera.Client
	snClient sdknet.SupernodeClient
}

func (c *CLI) parseCLIOptions() {
	pflag.StringVar(&c.opts.ConfigPath, "config", "", "Path to config file")
	pflag.StringVar(&c.opts.GrpcEndpoint, "grpc_endpoint", "", "Supernode gRPC endpoint")
	pflag.StringVar(&c.opts.SupernodeAddr, "address", "", "Supernode Lumera address")
	pflag.Parse()

	args := pflag.Args()
	if len(args) > 0 {
		c.opts.Command = args[0]
		c.opts.CommandArgs = args[1:]
	}
}

func NewCLI() *CLI {
	cli := &CLI{}
	return cli
}

func processConfigPath(path string) string {
	// expand environment variables if any
	path = os.ExpandEnv(path)
	// replaces ~ with the user's home directory
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Unable to resolve home directory: %v", err)
		}
		path = filepath.Join(home, path[1:])
	}
	// check if path defines directory
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, defaultConfigFileName)
	}
	path = filepath.Clean(path)
	return path
}

// detectConfigPath resolves the configuration file path based on:
// 1. CLI argument (--config) if provided
// 2. SNCLI_CONFIG_PATH environment variable
// 3. Default to ./config.toml
func (c *CLI) detectConfigPath() string {
	if c.opts.ConfigPath != "" {
		return processConfigPath(c.opts.ConfigPath)
	}
	if envPath := os.Getenv("SNCLI_CONFIG_PATH"); envPath != "" {
		return processConfigPath(envPath)
	}
	return defaultConfigFileName
}

func (c *CLI) loadCLIConfig() {
	path := c.detectConfigPath()
	_, err := toml.DecodeFile(path, &c.cfg)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", path, err)
	}
}

func (c *CLI) validateCLIConfig() {
	if c.opts.GrpcEndpoint != "" {
		c.cfg.Supernode.GRPCEndpoint = c.opts.GrpcEndpoint
	}
	if c.opts.SupernodeAddr != "" {
		c.cfg.Supernode.Address = c.opts.SupernodeAddr
	}
}

func (c *CLI) Initialize() {
	// Parse command-line options
	c.parseCLIOptions()
	// Load options from toml configuration file
	c.loadCLIConfig()
	// Validate configuration & override with CLI options if provided
	c.validateCLIConfig()
	
	// Initialize Supernode SDK
	snkeyring.InitSDKConfig()

	// Initialize keyring
	var err error
	c.kr, err = snkeyring.InitKeyring(c.cfg.Keyring.Backend, c.cfg.Keyring.Dir)
	if err != nil {
		log.Fatalf("Keyring init failed: %v", err)
	}

	// Create Lumera client adapter
	c.sdkConfig = sdkcfg.NewConfig(
		sdkcfg.AccountConfig{
			LocalCosmosAddress: c.cfg.Keyring.LocalAddress,
			KeyName:            c.cfg.Keyring.KeyName,
			Keyring:            c.kr,
		},
		sdkcfg.LumeraConfig{
			GRPCAddr: c.cfg.Lumera.GRPCAddr,
			ChainID:  c.cfg.Lumera.ChainID,
		},
	)

	c.lumeraClient, err = lumera.NewAdapter(context.Background(), lumera.ConfigParams{
		GRPCAddr: c.sdkConfig.Lumera.GRPCAddr,
		ChainID:  c.sdkConfig.Lumera.ChainID,
		KeyName:  c.sdkConfig.Account.KeyName,
		Keyring:  c.kr,
	}, sdklog.NewNoopLogger())
	if err != nil {
		log.Fatalf("Lumera client init failed: %v", err)
	}

	conn.RegisterALTSRecordProtocols()
}

func (c *CLI) Finalize() {
	conn.UnregisterALTSRecordProtocols()

	if c.snClient != nil {
		c.snClient.Close(context.Background())
	}	
}

func (c *CLI) snClientInit() {
	if c.snClient != nil {
		return // Already initialized
	}

	if c.cfg.Supernode.Address == "" || c.cfg.Supernode.GRPCEndpoint == "" {
		log.Fatal("Supernode address and gRPC endpoint must be configured")
	}

	supernode := lumera.Supernode{
		CosmosAddress: c.cfg.Supernode.Address,
		GrpcEndpoint:  c.cfg.Supernode.GRPCEndpoint,
	}

	clientFactory := sdknet.NewClientFactory(
		context.Background(),
		sdklog.NewNoopLogger(),
		c.kr,
		c.lumeraClient,
		sdknet.FactoryConfig{
			LocalCosmosAddress: c.cfg.Keyring.LocalAddress,
			PeerType:           1, // Simplenode
		},
	)

	var err error
	c.snClient, err = clientFactory.CreateClient(context.Background(), supernode)
	if err != nil {
		log.Fatalf("Supernode client init failed: %v", err)
	}
}
