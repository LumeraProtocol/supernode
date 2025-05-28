package config

import (
	"errors"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	cosmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// AccountConfig holds peer-to-peer addresses, ports, etc.
type AccountConfig struct {
	LocalCosmosAddress string
	KeyName            string
	Keyring            cosmoskeyring.Keyring
	PeerType           securekeyx.PeerType
}

// LumeraConfig wraps all chain-specific dials.
type LumeraConfig struct {
	GRPCAddr string
	ChainID  string
}

type Config struct {
	Account AccountConfig
	Lumera  LumeraConfig
}

func NewConfig(account AccountConfig, lumera LumeraConfig) Config {
	return Config{
		Account: account,
		Lumera:  lumera,
	}
}

func (c Config) Validate() error {
	switch {
	case c.Account.LocalCosmosAddress == "":
		return errors.New("config: Network.LocalCosmosAddress is required")
	case c.Lumera.GRPCAddr == "":
		return errors.New("config: Lumera.GRPCAddr is required")
	case c.Lumera.ChainID == "":
		return errors.New("config: Lumera.ChainID is required")
	case c.Account.PeerType == 0:
		return errors.New("config: Account.PeerType is required")
	case c.Account.KeyName == "":
		return errors.New("config: Account.KeyName is required")
	case c.Account.Keyring == nil:
		return errors.New("config: Account.Keyring is required")
	default:
		return nil
	}
}
