package lumera

import (
	"context"
	"fmt"
	
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"

	"cosmossdk.io/api/tendermint/types"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
)

func (c *Client) GetBlockByHeight(ctx context.Context, height int64) (Block, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:      "GetBlockByHeight",
		logtrace.FieldModule:      logtrace.ValueBaseTendermint,
		logtrace.FieldBlockHeight: height,
	}
	logtrace.Info(ctx, "fetching block by given height", fields)

	res, err := c.tendermintClient.GetBlockByHeight(ctx, &tendermintv1beta1.GetBlockByHeightRequest{
		Height: height,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return Block{}, fmt.Errorf("error fetching block by height: %w", err)
	}

	logtrace.Info(ctx, "block details retrieved", fields)

	return toBlock(res), nil
}

func toBlock(b interface{}) Block {
	var h *tendermintv1beta1.Header
	var blockID *types.BlockID

	switch v := b.(type) {
	case *tendermintv1beta1.GetLatestBlockResponse:
		h = v.GetSdkBlock().GetHeader()
		blockID = v.BlockId
	case *tendermintv1beta1.GetBlockByHeightResponse:
		h = v.GetSdkBlock().GetHeader()
		blockID = v.BlockId
	default:
		panic("toBlock: unexpected type provided")
	}

	return Block{
		Height:        h.Height,
		Hash:          blockID.Hash,
		LastBlockHash: h.LastBlockId.Hash,
		ChainID:       h.ChainId,
		DataHash:      h.DataHash,
		AppHash:       h.AppHash,
		Version:       fmt.Sprintf("Block Protocol: %v, App: %v", h.Version.Block, h.Version.App),
	}
}
