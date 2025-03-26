package config

import (
	"action/event"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Config holds configuration values for the ActionClient
type Config struct {
	// Security configuration
	Keyring            keyring.Keyring     // Keyring containing identity keys
	LocalCosmosAddress string              // Local cosmos address for authentication
	LocalPeerType      securekeyx.PeerType // Local peer type (Simplenode for clients)

	// Network configuration
	DefaultSupernodePort int // Default port for supernode gRPC endpoints

	// Task configuration
	MaxRetries            int // Maximum number of retries for supernode communication
	TimeoutSeconds        int // Timeout for supernode communication in seconds
	SenseSupernodeCount   int // Number of supernodes to select for Sense operations
	CascadeSupernodeCount int // Number of supernodes to select for Cascade operations

	// Event system
	EventBus *event.Bus // Event bus for task events
}

// DefaultConfig returns a Config with default values
func DefaultConfig() Config {
	return Config{
		LocalPeerType:         securekeyx.Simplenode,
		DefaultSupernodePort:  50051,
		MaxRetries:            3,
		TimeoutSeconds:        30,
		SenseSupernodeCount:   3,
		CascadeSupernodeCount: 1,
		EventBus:              event.NewBus(),
	}
}
