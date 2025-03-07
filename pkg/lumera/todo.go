package lumera

import (
	"context"
)

// GetRawTransactionVerbose1Result describes the result of "getrawtranasction txid 1"
type GetRawTransactionVerbose1Result struct {
	// Other information are omitted here because we don't care
	// They can be added later if there are business requirements
	Txid          string          `json:"txid"`
	Confirmations int64           `json:"confirmations"`
	Vout          []VoutTxnResult `json:"vout"`
	Time          int64           `json:"time"`
}

// VoutTxnResult is detail of txn
type VoutTxnResult struct {
	Value        float64      `json:"value"`
	ValuePat     int64        `json:"valuePat"`
	ScriptPubKey ScriptPubKey `json:"scriptPubKey"`
}

// ScriptPubKey lists addresses of txn
type ScriptPubKey struct {
	Addresses []string `json:"addresses"`
}

// TODO : implementation
func (c *Client) GetBlockCount(ctx context.Context) (int32, error) {
	return 0, nil
}

// TODO : implementation
func (c *Client) GetRawTransactionVerbose1(ctx context.Context, txID string) (*GetRawTransactionVerbose1Result, error) {
	return nil, nil
}

// TODO : implementation
func (c *Client) GetEstimatedCascadeFee(ctx context.Context, ImgSizeInMb float64) (float64, error) {
	return 0, nil
}

// TODO : implementation
func (c *Client) MasterNodesExtra(ctx context.Context) (SuperNodeAddressInfos, error) {
	return nil, nil
}

// TODO : implementation
func (c *Client) MasterNodesTop(ctx context.Context) (SuperNodeAddressInfos, error) {
	return nil, nil
}
