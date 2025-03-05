//go:generate mockgen -destination=client_mock.go -package=client -source=client.go

package client

import (
	"context"
	"fmt"

	cascadeService "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"google.golang.org/grpc"
)

type Client struct {
	conn           *grpc.ClientConn
	cascadeService cascadeService.CascadeServiceClient
}

type Service interface {
	UploadInputData(ctx context.Context, req UploadInputDataRequest) (UploadInputDataResponse, error)
}

func NewClient(serverAddr string) (Service, error) {
	conn, err := grpc.Dial(serverAddr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &Client{
		conn:           conn,
		cascadeService: cascadeService.NewCascadeServiceClient(conn),
	}, nil
}

func (c *Client) Close() {
	c.conn.Close()
}
