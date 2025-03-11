package tx

import (
	"context"
	"fmt"

	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	client sdktx.ServiceClient
}

// newModule creates a new Transaction module client
func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: sdktx.NewServiceClient(conn),
	}, nil
}

// BroadcastTx broadcasts a signed transaction
func (m *module) BroadcastTx(ctx context.Context, txBytes []byte, mode sdktx.BroadcastMode) (*sdktx.BroadcastTxResponse, error) {
	req := &sdktx.BroadcastTxRequest{
		TxBytes: txBytes,
		Mode:    mode,
	}

	resp, err := m.client.BroadcastTx(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	return resp, nil
}

// SimulateTx simulates a transaction
func (m *module) SimulateTx(ctx context.Context, txBytes []byte) (*sdktx.SimulateResponse, error) {
	req := &sdktx.SimulateRequest{
		TxBytes: txBytes,
	}

	resp, err := m.client.Simulate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to simulate transaction: %w", err)
	}

	return resp, nil
}

// GetTx gets transaction by hash
func (m *module) GetTx(ctx context.Context, hash string) (*sdktx.GetTxResponse, error) {
	req := &sdktx.GetTxRequest{
		Hash: hash,
	}

	resp, err := m.client.GetTx(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	return resp, nil
}
