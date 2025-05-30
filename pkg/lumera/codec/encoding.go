package codec

import (
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// EncodingConfig specifies the concrete encoding types to use for Lumera client
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

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
	authtypes.RegisterInterfaces(registry)
	actiontypes.RegisterInterfaces(registry)
	// Add more interface registrations here as you add more modules
}

// GetEncodingConfig returns the standard encoding config for Lumera client
func GetEncodingConfig() EncodingConfig {
	return NewEncodingConfig()
}
