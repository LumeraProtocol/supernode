package tx

import (
	"context"
	"fmt"
	"time"

	txmodule "github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"

	"github.com/LumeraProtocol/supernode/gen/lumera/action/types"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	client   types.MsgClient
	txModule txmodule.Module
}

// newModule creates a new Action tx module client
func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	txMod, err := txmodule.NewModule(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create tx module: %w", err)
	}

	return &module{
		client:   types.NewMsgClient(conn),
		txModule: txMod,
	}, nil
}

// FinalizeAction finalizes the given action
func (m *module) FinalizeAction(
	ctx context.Context,
	txConfig client.TxConfig,
	keyRing cKeyring.Keyring,
	actionID, creator, actionType, metadata, rpcURL, chainID string,
) (*types.MsgFinalizeActionResponse, error) {
	fromName := creator // assuming `creator` is the key name in keyring
	info, err := keyRing.Key(fromName)
	if err != nil {
		return nil, fmt.Errorf("failed to get key info: %w", err)
	}
	fromAddr, err := info.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key: %w", err)
	}

	_ = client.Context{}.
		WithNodeURI(rpcURL).
		WithChainID(chainID).
		WithKeyring(keyRing).
		WithFromName(fromName).
		WithFromAddress(fromAddr).
		WithTxConfig(txConfig)

	msg := &types.MsgFinalizeAction{
		ActionId:   actionID,
		Creator:    creator,
		ActionType: actionType,
		Metadata:   metadata,
	}

	txf := tx.Factory{}.
		WithChainID(chainID).
		WithKeybase(keyRing).
		WithTxConfig(txConfig).
		WithGasAdjustment(1.2).
		WithGasPrices("0.025stake")

	builder := txConfig.NewTxBuilder()
	if err := builder.SetMsgs(msg); err != nil {
		return nil, fmt.Errorf("failed to set message: %w", err)
	}
	builder.SetGasLimit(0) // zero for simulation

	gasCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	txf = txf.WithGas(0)

	simBuilder := txConfig.NewTxBuilder()
	if err := simBuilder.SetMsgs(msg); err != nil {
		return nil, fmt.Errorf("failed to set messages for simulation: %w", err)
	}

	if err := tx.Sign(gasCtx, txf, fromName, simBuilder, true); err != nil {
		return nil, fmt.Errorf("failed to sign transaction for simulation: %w", err)
	}

	simBytes, err := txConfig.TxEncoder()(simBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode tx for simulation: %w", err)
	}

	simRes, err := m.txModule.SimulateTx(gasCtx, simBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to simulate transaction: %w", err)
	}

	estimatedGas := simRes.GasInfo.GasUsed
	builder.SetGasLimit(uint64(float64(estimatedGas) * txf.GasAdjustment()))

	gasPrices := txf.GasPrices()
	if len(gasPrices) > 0 {
		var fees sdk.Coins
		for _, gasPrice := range gasPrices {
			feeAmount := gasPrice.Amount.MulInt64(int64(builder.GetTx().GetGas()))
			fees = append(fees, sdk.NewCoin(gasPrice.Denom, feeAmount.TruncateInt()))
		}

		builder.SetFeeAmount(fees)
	}

	if err := tx.Sign(ctx, txf, fromName, builder, true); err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	txBytes, err := txConfig.TxEncoder()(builder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode tx: %w", err)
	}

	_, err = m.txModule.BroadcastTx(ctx, txBytes, sdktx.BroadcastMode_BROADCAST_MODE_BLOCK)
	if err != nil {
		return nil, fmt.Errorf("failed to broadcast tx: %w", err)
	}

	return &types.MsgFinalizeActionResponse{}, nil
}
