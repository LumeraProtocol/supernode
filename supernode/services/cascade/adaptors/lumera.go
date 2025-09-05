package adaptors

import (
	"context"
	"fmt"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

//go:generate mockgen -destination=mocks/lumera_mock.go -package=cascadeadaptormocks -source=lumera.go

// LumeraClient defines the interface for interacting with Lumera chain data during cascade registration.
type LumeraClient interface {
	// SupernodeModule
	GetTopSupernodes(ctx context.Context, height uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error)

	// Action Module
	GetAction(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error)
	FinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.BroadcastTxResponse, error)
	SimulateFinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.SimulateResponse, error)
	GetActionFee(ctx context.Context, dataSize string) (*actiontypes.QueryGetActionFeeResponse, error)
	// Auth
	Verify(ctx context.Context, creator string, file []byte, sigBytes []byte) error
}

// Client is the concrete implementation used in production.
type Client struct {
	lc lumera.Client
}

func NewLumeraClient(client lumera.Client) LumeraClient {
	return &Client{
		lc: client,
	}
}

func (c *Client) GetAction(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error) {
	return c.lc.Action().GetAction(ctx, actionID)
}

func (c *Client) GetActionFee(ctx context.Context, dataSize string) (*actiontypes.QueryGetActionFeeResponse, error) {
	return c.lc.Action().GetActionFee(ctx, dataSize)
}

func (c *Client) FinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.BroadcastTxResponse, error) {
	resp, err := c.lc.ActionMsg().FinalizeCascadeAction(ctx, actionID, rqids)
	if err != nil {
		// Preserve underlying gRPC status/details
		return nil, fmt.Errorf("finalize cascade action broadcast failed: %w", err)
	}

	// Surface chain-level failures (non-zero code) with rich context
	if resp != nil && resp.TxResponse != nil && resp.TxResponse.Code != 0 {
		return nil, fmt.Errorf(
			"tx failed: code=%d codespace=%s height=%d gas_wanted=%d gas_used=%d raw_log=%s",
			resp.TxResponse.Code,
			resp.TxResponse.Codespace,
			resp.TxResponse.Height,
			resp.TxResponse.GasWanted,
			resp.TxResponse.GasUsed,
			resp.TxResponse.RawLog,
		)
	}

	return resp, nil
}

func (c *Client) SimulateFinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.SimulateResponse, error) {
	return c.lc.ActionMsg().SimulateFinalizeCascadeAction(ctx, actionID, rqids)
}

func (c *Client) GetTopSupernodes(ctx context.Context, height uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return c.lc.SuperNode().GetTopSuperNodesForBlock(ctx, height)
}

func (c *Client) Verify(ctx context.Context, creator string, file []byte, sigBytes []byte) error {
	return c.lc.Auth().Verify(ctx, creator, file, sigBytes)
}
