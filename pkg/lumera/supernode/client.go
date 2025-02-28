//go:generate mockgen -destination=supernode_mock.go -package=supernode -source=client.go

package supernode

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	lumerasn "github.com/LumeraProtocol/lumera/x/supernode/types"
)

type Client struct {
	conn             *grpc.ClientConn
	supernodeService lumerasn.QueryClient
}

type Service interface {
	GetTopSNsByBlockHeight(ctx context.Context, r GetTopSupernodesForBlockRequest) (GetTopSupernodesForBlockResponse, error)
	GetSupernodeBySupernodeAddress(ctx context.Context, r GetSupernodeBySuperNodeAddressRequest) (Supernode, error)
}

func NewClient(serverAddr string) (Service, error) {
	conn, err := grpc.Dial(serverAddr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &Client{
		conn:             conn,
		supernodeService: lumerasn.NewQueryClient(conn),
	}, nil
}

func (c *Client) Close() {
	c.conn.Close()
}
