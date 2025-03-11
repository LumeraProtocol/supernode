package lumera

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
)

const SeparatorByte byte = 46

func (c *Client) Verify(ctx context.Context, accAddress string, data, signature []byte) (err error) {
	addr, err := types.AccAddressFromBech32(accAddress)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}

	keyInfo, err := c.cosmosSdk.Keyring.KeyByAddress(addr)
	if err != nil {
		return fmt.Errorf("address not found in keyring: %w", err)
	}

	pubKey, err := keyInfo.GetPubKey()
	if err != nil {
		return fmt.Errorf("failed to get public key: %w", err)
	}

	if !pubKey.VerifySignature(data, signature) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}
