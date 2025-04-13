package cascade

import (
	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

// Config contains settings for the cascade service
type Config struct {
	common.Config `mapstructure:",squash" json:"-"`

	RaptorQServiceAddress string `mapstructure:"-" json:"-"`
	RqFilesDir            string `mapstructure:"rq_files_dir" json:"rq_files_dir,omitempty"`
}

// NewConfig returns a new Config instance with default values
func NewConfig() *Config {
	return &Config{}
}
