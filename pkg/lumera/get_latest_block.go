package lumera

import (
	"context"
	"fmt"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
)

type Block struct {
	Height  int64  `json:"height"`
	ChainID string `json:"chain_id"`
	Version string `json:"version"`

	Hash          []byte `json:"block_hash"`
	LastBlockHash []byte `json:"last_block_hash"`
	DataHash      []byte `json:"data_hash"`
	AppHash       []byte `json:"app_hash"`
}

func (c *Client) GetLatestBlock(ctx context.Context) (Block, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod: "GetLatestBlock",
		logtrace.FieldModule: logtrace.ValueBaseTendermint,
	}
	logtrace.Info(ctx, "fetching latest block", fields)

	res, err := c.tendermintClient.GetLatestBlock(ctx, &tendermintv1beta1.GetLatestBlockRequest{})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return Block{}, fmt.Errorf("error fetching latest block: %w", err)
	}

	logtrace.Info(ctx, "block details retrieved", fields)

	return toBlock(res), nil
}
