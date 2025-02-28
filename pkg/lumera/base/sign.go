package base

import (
	"context"
	"fmt"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/types"
)

func (c *Client) Sign(ctx context.Context, senderName string, msgs []types.Msg) ([]byte, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:  "Sign",
		logtrace.FieldModule:  logtrace.ValueTransaction,
		logtrace.FieldRequest: msgs,
	}

	txf := tx.Factory{}.
		WithKeybase(c.cosmosSdk.Keyring).
		WithChainID(c.cosmosSdk.ChainID)

	txBuilder, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, fmt.Errorf("build tx error: %w", err)
	}
	logtrace.Info(ctx, "build unsigned transaction", fields)

	if err := tx.Sign(ctx, txf, senderName, txBuilder, true); err != nil {
		return nil, fmt.Errorf("sign tx error: %w", err)
	}
	logtrace.Info(ctx, "transaction has been signed", fields)

	txBytes, err := c.cosmosSdk.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("encode tx error: %w", err)
	}
	logtrace.Info(ctx, "transaction has been encoded", fields)

	return txBytes, nil
}
