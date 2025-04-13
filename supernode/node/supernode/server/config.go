package server

const (
	defaultListenAddresses = "0.0.0.0"
	defaultPort            = 4444
)

// Config contains settings of the supernode server.
type Config struct {
	Identity        string
	ListenAddresses string
	Port            int
}

// NewConfig returns a new Config instance.
func NewConfig() *Config {
	return &Config{
		ListenAddresses: defaultListenAddresses,
		Port:            defaultPort,
	}
}
