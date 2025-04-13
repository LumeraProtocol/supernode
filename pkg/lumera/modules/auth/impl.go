package auth

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"

	// cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
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

	accInfo, err := m.AccountInfoByAddress(ctx, addr.String())
	if err != nil {
		return fmt.Errorf("account info: %w", err)
	}

	pubKey := accInfo.Info.GetPubKey()
	if pubKey == nil {
		return fmt.Errorf("public key is nil")
	}

	if !pubKey.VerifySignature(data, signature) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}
