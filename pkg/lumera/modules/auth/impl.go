package auth

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	client authtypes.QueryClient
}

// newModule creates a new auth module client
func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: authtypes.NewQueryClient(conn),
	}, nil
}

// AccountInfoByAddress gets the account info by address
func (m *module) AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error) {
	accountResp, err := m.client.AccountInfo(ctx, &authtypes.QueryAccountInfoRequest{
		Address: addr,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	return accountResp, nil
}

func (m *module) Verify(ctx context.Context, accAddress string, data, signature []byte) (err error) {
	// Validate the address
	addr, err := types.AccAddressFromBech32(accAddress)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}

	logtrace.Info(ctx, "Verifying signature", logtrace.Fields{"address": addr.String()})

	// Use Account RPC instead of AccountInfo to get the full account with public key
	accResp, err := m.client.Account(ctx, &authtypes.QueryAccountRequest{
		Address: addr.String(),
	})
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	// Unpack the account from Any type
	var account types.AccountI
	if err := m.getEncodingConfig().InterfaceRegistry.UnpackAny(accResp.Account, &account); err != nil {
		return fmt.Errorf("failed to unpack account: %w", err)
	}

	pubKey := account.GetPubKey()
	if pubKey == nil {
		return fmt.Errorf("public key is nil")
	}
	logtrace.Info(ctx, "Public key retrieved", logtrace.Fields{"pubKey": pubKey.String()})
	if !pubKey.VerifySignature(data, signature) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// getEncodingConfig returns the module's encoding config
func (m *module) getEncodingConfig() EncodingConfig {
	amino := codec.NewLegacyAmino()

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	authtypes.RegisterInterfaces(interfaceRegistry)

	marshaler := codec.NewProtoCodec(interfaceRegistry)

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             marshaler,
		Amino:             amino,
	}
}

// EncodingConfig specifies the concrete encoding types to use
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	Amino             *codec.LegacyAmino
}
