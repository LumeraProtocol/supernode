package lumera

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Config holds all the configuration needed for the client
type Config struct {
	// GRPCAddr is the gRPC endpoint address
	GRPCAddr string

	// ChainID is the ID of the chain
	ChainID string

	// keyring is the keyring conf for the node sign & verify
	keyring keyring.Keyring

	// KeyName is the name of the key to use for signing
	KeyName string
}

func NewConfig(grpcAddr, chainID string, keyName string, keyring keyring.Keyring) (*Config, error) {

	if grpcAddr == "" {
		return nil, fmt.Errorf("grpcAddr cannot be empty")
	}
	if chainID == "" {
		return nil, fmt.Errorf("chainID cannot be empty")
	}
	if keyring == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}
	if keyName == "" {
		return nil, fmt.Errorf("keyName cannot be empty")
	}

	return &Config{
		GRPCAddr: grpcAddr,
		ChainID:  chainID,
		keyring:  keyring,
		KeyName:  keyName,
	}, nil
}
