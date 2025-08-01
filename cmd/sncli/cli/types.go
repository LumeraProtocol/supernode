package cli

type CLIOptions struct {
	ConfigPath    string
	GrpcEndpoint  string
	SupernodeAddr string

	Command       string
	CommandArgs   []string
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
	} `toml:"keyring"`

	Supernode struct {
		GRPCEndpoint string `toml:"grpc_endpoint"`
		Address      string `toml:"address"`
	} `toml:"supernode"`
}
