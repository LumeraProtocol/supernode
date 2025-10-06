package bank

import (
	"context"
	"fmt"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
)

type module struct {
	client banktypes.QueryClient
}

func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}
	return &module{client: banktypes.NewQueryClient(conn)}, nil
}

func (m *module) Balance(ctx context.Context, address string, denom string) (*banktypes.QueryBalanceResponse, error) {
	if address == "" {
		return nil, fmt.Errorf("address cannot be empty")
	}
	if denom == "" {
		return nil, fmt.Errorf("denom cannot be empty")
	}
	return m.client.Balance(ctx, &banktypes.QueryBalanceRequest{Address: address, Denom: denom})
}
