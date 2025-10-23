package adaptors

import (
	"context"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

type LumeraClient interface {
	GetAction(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error)
	GetTopSupernodes(ctx context.Context, blockHeight uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error)
	Verify(ctx context.Context, address string, msg []byte, sig []byte) error
	GetActionFee(ctx context.Context, dataSizeKB string) (*actiontypes.QueryGetActionFeeResponse, error)
	SimulateFinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.SimulateResponse, error)
	FinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.BroadcastTxResponse, error)
}

type lumeraImpl struct{ c lumera.Client }

func NewLumeraClient(c lumera.Client) LumeraClient { return &lumeraImpl{c: c} }

func (l *lumeraImpl) GetAction(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error) {
	return l.c.Action().GetAction(ctx, actionID)
}

func (l *lumeraImpl) GetTopSupernodes(ctx context.Context, blockHeight uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return l.c.SuperNode().GetTopSuperNodesForBlock(ctx, blockHeight)
}

func (l *lumeraImpl) Verify(ctx context.Context, address string, msg []byte, sig []byte) error {
	return l.c.Auth().Verify(ctx, address, msg, sig)
}

func (l *lumeraImpl) GetActionFee(ctx context.Context, dataSizeKB string) (*actiontypes.QueryGetActionFeeResponse, error) {
	return l.c.Action().GetActionFee(ctx, dataSizeKB)
}

func (l *lumeraImpl) SimulateFinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.SimulateResponse, error) {
	return l.c.ActionMsg().SimulateFinalizeCascadeAction(ctx, actionID, rqids)
}

func (l *lumeraImpl) FinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.BroadcastTxResponse, error) {
	return l.c.ActionMsg().FinalizeCascadeAction(ctx, actionID, rqids)
}
