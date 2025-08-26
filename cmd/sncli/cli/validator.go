package cli

import (
	"context"
	"fmt"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
)

type SecureKeyExchangeValidator struct {
	lumeraClient lumera.Client
}

func NewSecureKeyExchangeValidator(lumeraClient lumera.Client) *SecureKeyExchangeValidator {
	return &SecureKeyExchangeValidator{
		lumeraClient: lumeraClient,
	}
}

func (v *SecureKeyExchangeValidator) AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error) {
	accountInfo, err := v.lumeraClient.AccountInfoByAddress(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}
	return accountInfo, nil
}

func (v *SecureKeyExchangeValidator) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*sntypes.SuperNode, error) {
	supernodeInfo, err := v.lumeraClient.GetSupernodeBySupernodeAddress(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode info: %w", err)
	}
	if supernodeInfo == nil {
		return nil, fmt.Errorf("supernode info is nil")
	}
	return supernodeInfo, nil
}
