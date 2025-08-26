package cli

type CLIOptions struct {
	ConfigPath    string
	GrpcEndpoint  string
	SupernodeAddr string
	P2PEndpoint   string
}

type CLIConfig struct {
	Lumera struct {
		GRPCAddr string `toml:"grpc_addr"`
		ChainID  string `toml:"chain_id"`
	} `toml:"lumera"`

	Keyring struct {
		Backend      string `toml:"backend"`
		Dir          string `toml:"dir"`
		KeyName      string `toml:"key_name"`
		LocalAddress string `toml:"local_address"`
		PassPlain    string `toml:"passphrase_plain"`
		PassFile     string `toml:"passphrase_file"`
		PassEnv      string `toml:"passphrase_env"`
	} `toml:"keyring"`

	Supernode struct {
		GRPCEndpoint string `toml:"grpc_endpoint"`
		P2PEndpoint   string `toml:"p2p_endpoint"`
		Address      string `toml:"address"`
	} `toml:"supernode"`
}

func (c *CLIConfig) GetLocalIdentity() string {
	return c.Keyring.LocalAddress
}

func (c *CLIConfig) GetRemoteIdentity() string {
	return c.Supernode.Address
}