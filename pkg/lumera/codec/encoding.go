package codec

import (
	"sync"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	evmigrationtypes "github.com/LumeraProtocol/lumera/x/evmigration/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	evmcryptocodec "github.com/cosmos/evm/crypto/codec"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	vestingtypes "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
)

// EncodingConfig specifies the concrete encoding types to use for Lumera client
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

var (
	encOnce sync.Once
	encCfg  EncodingConfig
)

// NewEncodingConfig creates a new EncodingConfig with all required interfaces registered
func NewEncodingConfig() EncodingConfig {
	amino := codec.NewLegacyAmino()
	interfaceRegistry := codectypes.NewInterfaceRegistry()

	// Register all required interfaces
	RegisterInterfaces(interfaceRegistry)

	marshaler := codec.NewProtoCodec(interfaceRegistry)
	txConfig := authtx.NewTxConfig(marshaler, authtx.DefaultSignModes)

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             marshaler,
		TxConfig:          txConfig,
		Amino:             amino,
	}
}

// RegisterInterfaces registers all interface types with the interface registry
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	cryptocodec.RegisterInterfaces(registry)
	evmcryptocodec.RegisterInterfaces(registry)
	authtypes.RegisterInterfaces(registry)
	vestingtypes.RegisterInterfaces(registry)
	actiontypes.RegisterInterfaces(registry)
	evmigrationtypes.RegisterInterfaces(registry)
	sntypes.RegisterInterfaces(registry)
}

// GetEncodingConfig returns the standard encoding config for Lumera client
func GetEncodingConfig() EncodingConfig {
	// Cache the encoding config to avoid repeated allocations and registrations
	// across transactions and simulations.
	encOnce.Do(func() { encCfg = NewEncodingConfig() })
	return encCfg
}
