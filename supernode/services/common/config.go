package common

const (
	defaultNumberSuperNodes = 10
)

// Config contains common configuration of the services.
type Config struct {
	SupernodeAccountAddress string
	SupernodeIPAddress      string
	NumberSuperNodes        int
}

// NewConfig returns a new Config instance
func NewConfig() *Config {
	return &Config{
		NumberSuperNodes: defaultNumberSuperNodes,
	}
}
