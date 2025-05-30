package adaptors

import (
	"context"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
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
	return c.lc.ActionMsg().FinalizeCascadeAction(ctx, actionID, rqids)
}

func (c *Client) GetTopSupernodes(ctx context.Context, height uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return c.lc.SuperNode().GetTopSuperNodesForBlock(ctx, height)
}

func (c *Client) Verify(ctx context.Context, creator string, file []byte, sigBytes []byte) error {
	return c.lc.Auth().Verify(ctx, creator, file, sigBytes)
}
