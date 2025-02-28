package lumera

import (
	"context"
	"fmt"
	"github.com/LumeraProtocol/supernode/pkg/net"

	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

type BroadcastMode string

const (
	BroadcastModeSync  BroadcastMode = "sync"
	BroadcastModeAsync BroadcastMode = "async"
	BroadcastModeBlock BroadcastMode = "block"
)

type BroadcastRequest struct {
	TxBytes []byte        `json:"tx_bytes"`
	Mode    BroadcastMode `json:"mode"`
}

type BroadcastResponse struct {
	TxHash string `json:"tx_hash"`
}

func (c *Client) BroadcastTx(ctx context.Context, req BroadcastRequest) (BroadcastResponse, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:  "Broadcast",
		logtrace.FieldModule:  logtrace.ValueTransaction,
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "broadcasting tx", fields)

	res, err := c.txClient.BroadcastTx(ctx, &tx.BroadcastTxRequest{
		TxBytes: req.TxBytes,
		Mode:    req.Mode.toTxBroadcastMode(),
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return BroadcastResponse{}, fmt.Errorf("broadcast tx error: %w", err)
	}

	logtrace.Info(ctx, "tx broadcasted", fields)

	return BroadcastResponse{TxHash: res.String()}, nil
}

func (m BroadcastMode) toTxBroadcastMode() tx.BroadcastMode {
	switch m {
	case BroadcastModeAsync:
		return tx.BroadcastMode_BROADCAST_MODE_ASYNC
	case BroadcastModeSync:
		return tx.BroadcastMode_BROADCAST_MODE_SYNC
	case BroadcastModeBlock:
		return tx.BroadcastMode_BROADCAST_MODE_BLOCK
	default:
		return tx.BroadcastMode_BROADCAST_MODE_ASYNC
	}
}
