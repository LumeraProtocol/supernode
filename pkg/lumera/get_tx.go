package lumera

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type GetTxRequest struct {
	TxHash string `json:"tx_hash"`
}

type TxResponse struct {
	Height    int64  `json:"height,omitempty"`
	TxHash    string `json:"txhash,omitempty"`
	Codespace string `json:"codespace,omitempty"`
	Code      uint32 `json:"code,omitempty"`
	Data      string `json:"data,omitempty"`
	RawLog    string `json:"raw_log,omitempty"`
	Info      string `json:"info,omitempty"`
	GasWanted int64  `json:"gas_wanted,omitempty"`
	GasUsed   int64  `json:"gas_used,omitempty"`
	Tx        []byte `json:"tx,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// GetTxResponse is the response type for the Service.GetTx method.
type GetTxResponse struct {
	Tx         *tx.Tx      `json:"tx,omitempty"`
	TxResponse *TxResponse `json:"tx_response,omitempty"`
}

func (c *Client) GetTx(ctx context.Context, req GetTxRequest) (GetTxResponse, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:  "GetTx",
		logtrace.FieldModule:  logtrace.ValueTransaction,
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "fetching tx", fields)

	res, err := c.txClient.GetTx(ctx, &tx.GetTxRequest{
		Hash: req.TxHash,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return GetTxResponse{}, fmt.Errorf("error fetching tx: %w", err)
	}

	logtrace.Info(ctx, "tx retrieved", fields)

	return toGetTxResponse(res), nil
}

func toGetTxResponse(sdkResp *tx.GetTxResponse) GetTxResponse {
	return GetTxResponse{
		TxResponse: &TxResponse{
			Height:    sdkResp.TxResponse.Height,
			TxHash:    sdkResp.TxResponse.TxHash,
			Codespace: sdkResp.TxResponse.Codespace,
			Code:      sdkResp.TxResponse.Code,
			Data:      sdkResp.TxResponse.Data,
			RawLog:    sdkResp.TxResponse.RawLog,
			Info:      sdkResp.TxResponse.Info,
			GasWanted: sdkResp.TxResponse.GasWanted,
			GasUsed:   sdkResp.TxResponse.GasUsed,
			Timestamp: sdkResp.TxResponse.Timestamp,
		},
	}
}
