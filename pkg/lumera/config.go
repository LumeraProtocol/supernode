package lumera

import (
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// TxOptions holds the tunable gas/fee parameters used by the Lumera client
// when building, signing, and broadcasting transactions. Zero-valued fields
// are replaced by the package defaults declared in pkg/lumera/modules/tx.
//
// Intended use: operators who want to lower fee spend (or raise it for
// safety) without a code change can populate these via the supernode YAML
// config under `lumera.tx_*` keys.
type TxOptions struct {
	// GasAdjustment multiplies simulated gas to derive gas_wanted.
	// Lower → smaller fee but higher OOG risk. Default: 1.3.
	GasAdjustment float64
	// GasAdjustmentMultiplier is applied to GasAdjustment on each OOG
	// retry. Must be >1. Default: 1.3.
	GasAdjustmentMultiplier float64
	// GasAdjustmentMaxAttempts is the total number of attempts
	// (initial + retries) before giving up on an OOG failure. Default: 3.
	GasAdjustmentMaxAttempts int
	// GasPadding is added to gas_wanted after gas_adjustment. Default: 50000.
	GasPadding uint64
	// GasPrice is the unit fee (e.g. "0.025" or "0.025ulume"). Default: "0.025".
	GasPrice string
	// FeeDenom is the coin denom used for fees. Default: "ulume".
	FeeDenom string
}

// toTxHelperConfig materializes a tx.TxHelperConfig from the Config. Zero-valued
// options remain zero so tx.NewTxHelper applies defaults deterministically.
func (c *Config) toTxHelperConfig() *tx.TxHelperConfig {
	return &tx.TxHelperConfig{
		ChainID:                  c.ChainID,
		Keyring:                  c.keyring,
		KeyName:                  c.KeyName,
		GasAdjustment:            c.TxOptions.GasAdjustment,
		GasAdjustmentMultiplier:  c.TxOptions.GasAdjustmentMultiplier,
		GasAdjustmentMaxAttempts: c.TxOptions.GasAdjustmentMaxAttempts,
		GasPadding:               c.TxOptions.GasPadding,
		GasPrice:                 c.TxOptions.GasPrice,
		FeeDenom:                 c.TxOptions.FeeDenom,
	}
}

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

	// TxOptions tunes fee/gas behavior for outbound transactions. All fields
	// are optional; zero-valued fields fall back to pkg/lumera/modules/tx
	// defaults.
	TxOptions TxOptions
}

// NewConfig creates a client config using default TxOptions. Additional TxOptions
// can be supplied; the last one wins. Keeping TxOptions variadic preserves
// source-level compatibility with existing callers.
func NewConfig(grpcAddr, chainID string, keyName string, kr keyring.Keyring, opts ...TxOptions) (*Config, error) {
	if grpcAddr == "" {
		return nil, fmt.Errorf("grpcAddr cannot be empty")
	}
	if chainID == "" {
		return nil, fmt.Errorf("chainID cannot be empty")
	}
	if kr == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}
	if keyName == "" {
		return nil, fmt.Errorf("keyName cannot be empty")
	}

	cfg := &Config{
		GRPCAddr: grpcAddr,
		ChainID:  chainID,
		keyring:  kr,
		KeyName:  keyName,
	}
	for _, o := range opts {
		cfg.TxOptions = o
	}
	return cfg, nil
}
