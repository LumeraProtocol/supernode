package cascade

import (
	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

const (
	defaultNumberConnectedNodes       = 2
	defaultPreburntTxMinConfirmations = 3
)

// Config contains settings of the registering Nft.
type Config struct {
	common.Config `mapstructure:",squash" json:"-"`

	RaptorQServiceAddress string `mapstructure:"-" json:"-"`
	RaptorQServicePort    string `mapstructure:"-" json:"-"`
	RqFilesDir            string

	NumberConnectedNodes int `mapstructure:"-" json:"number_connected_nodes,omitempty"`
}

// NewConfig returns a new Config instance.
func NewConfig() *Config {
	return &Config{
		NumberConnectedNodes: defaultNumberConnectedNodes,
	}
}
