package cli

import (
	"context"
	"log"
	"os"
	"fmt"
	"time"

	"github.com/BurntSushi/toml"

	snkeyring "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials/alts/conn"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	sdkcfg "github.com/LumeraProtocol/supernode/v2/sdk/config"
	sdklog "github.com/LumeraProtocol/supernode/v2/sdk/log"
	sdknet "github.com/LumeraProtocol/supernode/v2/sdk/net"
	snconfig "github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

const (
	// defaultConfigFileName is the default path to the configuration file.
	defaultConfigFileName = "config.toml"

	// defaultConfigFolder is the default folder for configuration files.	
	defaultConfigFolder = "~/.sncli"
)

type CLI struct {
	cliOpts      CLIOptions
	CfgOpts      *CLIConfig
	ConfigPath   string
	kr           keyring.Keyring
	SdkConfig    sdkcfg.Config
	lumeraClient lumera.Client
	snClient     sdknet.SupernodeClient
	p2p          *P2P
}

func NewCLI() *CLI {
	cli := &CLI{}
	return cli
}

func (c *CLI) SetOptions(o CLIOptions) {
	c.cliOpts = o
}

func (c *CLI) SetP2P(p *P2P) {
	c.p2p = p
}

func (c *CLI) P2P() *P2P {
	return c.p2p
}

// detectConfigPath resolves the configuration file path based on:
// 1. CLI argument (--config) if provided
// 2. SNCLI_CONFIG_PATH environment variable
// 3. Default to ~/.sncli/config.toml
func (c *CLI) detectConfigPath() string {
	if c.cliOpts.ConfigPath != "" {
		return processConfigPath(c.cliOpts.ConfigPath)
	}
	if envPath := os.Getenv("SNCLI_CONFIG_PATH"); envPath != "" {
		return processConfigPath(envPath)
	}
	return processConfigPath(defaultConfigFolder)
}

func (c *CLI) loadCLIConfig() error {
	path := c.detectConfigPath()
	_, err := toml.DecodeFile(path, &c.CfgOpts)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("Config file not found at %s", path)
		}
		return fmt.Errorf("Failed to load config from %s: %v", path, err)
	}
	c.ConfigPath = path

	// override Supernode GRPC endpoint and address if provided in CLI options
	if c.cliOpts.GrpcEndpoint != "" {
		c.CfgOpts.Supernode.GRPCEndpoint = c.cliOpts.GrpcEndpoint
	}
	if c.cliOpts.SupernodeAddr != "" {
		c.CfgOpts.Supernode.Address = c.cliOpts.SupernodeAddr
	}
	if c.cliOpts.P2PEndpoint != "" {
		c.CfgOpts.Supernode.P2PEndpoint = c.cliOpts.P2PEndpoint
	}
	return nil
}

func (c *CLI) Initialize() error {
	var err error

	// Load options from toml configuration file
	// Validate configuration & override with CLI options if provided
	if err = c.loadCLIConfig(); err != nil {
		return err
	}

	// Initialize Supernode SDK
	snkeyring.InitSDKConfig()

	// Initialize keyring
	var krConfig snconfig.KeyringConfig
	krConfig.Backend = c.CfgOpts.Keyring.Backend
	krConfig.Dir = c.CfgOpts.Keyring.Dir
	krConfig.PassPlain = c.CfgOpts.Keyring.PassPlain
	krConfig.PassEnv = c.CfgOpts.Keyring.PassEnv
	krConfig.PassFile = c.CfgOpts.Keyring.PassFile
	c.kr, err = snkeyring.InitKeyring(krConfig)
	if err != nil {
		log.Fatalf("Keyring init failed: %v", err)
	}

	// Create Lumera client adapter
	c.SdkConfig = sdkcfg.NewConfig(
		sdkcfg.AccountConfig{
			LocalCosmosAddress: c.CfgOpts.Keyring.LocalAddress,
			KeyName:            c.CfgOpts.Keyring.KeyName,
			Keyring:            c.kr,
		},
		sdkcfg.LumeraConfig{
			GRPCAddr: c.CfgOpts.Lumera.GRPCAddr,
			ChainID:  c.CfgOpts.Lumera.ChainID,
		},
	)

	c.lumeraClient, err = lumera.NewAdapter(context.Background(), lumera.ConfigParams{
		GRPCAddr: c.SdkConfig.Lumera.GRPCAddr,
		ChainID:  c.SdkConfig.Lumera.ChainID,
		KeyName:  c.SdkConfig.Account.KeyName,
		Keyring:  c.kr,
	}, sdklog.NewNoopLogger())
	if err != nil {
		log.Fatalf("Lumera client init failed: %v", err)
	}

	conn.RegisterALTSRecordProtocols()

	return nil
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

	if c.CfgOpts.Supernode.Address == "" || c.CfgOpts.Supernode.GRPCEndpoint == "" {
		log.Fatal("Supernode address and gRPC endpoint must be configured")
	}

	supernode := lumera.Supernode{
		CosmosAddress: c.CfgOpts.Supernode.Address,
		GrpcEndpoint:  c.CfgOpts.Supernode.GRPCEndpoint,
	}

	clientFactory := sdknet.NewClientFactory(
		context.Background(),
		sdklog.NewNoopLogger(),
		c.kr,
		c.lumeraClient,
		sdknet.FactoryConfig{
			LocalCosmosAddress: c.CfgOpts.Keyring.LocalAddress,
			PeerType:           1, // Simplenode
		},
	)

	var err error
	c.snClient, err = clientFactory.CreateClient(context.Background(), supernode)
	if err != nil {
		log.Fatalf("Supernode client init failed: %v", err)
	}
}

func (c *CLI) P2PPing(timeout time.Duration) error {
	if c.p2p == nil {
		return fmt.Errorf("P2P: not initialized")
	}
	return c.p2p.Ping(timeout)
}