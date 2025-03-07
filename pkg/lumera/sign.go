package lumera

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
)

func (c *Client) Sign(ctx context.Context, snAccAddress string, data []byte) (signature []byte, err error) {
	accAddr, err := types.AccAddressFromBech32(snAccAddress)
	if err != nil {
		return signature, fmt.Errorf("invalid address: %w", err)
	}

	_, err = c.cosmosSdk.Keyring.KeyByAddress(accAddr)
	if err != nil {
		return signature, fmt.Errorf("address not found in keyring: %w", err)
	}

	signature, _, err = c.cosmosSdk.Keyring.SignByAddress(accAddr, data, signingtypes.SignMode_SIGN_MODE_DIRECT)
	if err != nil {
		return nil, fmt.Errorf("failed to sign data: %w", err)
	}

	return signature, nil
}
