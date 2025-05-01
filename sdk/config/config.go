package config

type Config struct {
	Network struct {
		DefaultSupernodePort int
		LocalCosmosAddress   string
	}

	Lumera struct {
		GRPCAddr string
		ChainID  string
		Timeout  int
		KeyName  string
	}
}
