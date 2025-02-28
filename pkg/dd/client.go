//go:generate mockgen -destination=dd_mock.go -package=dd -source=client.go

package dd

import (
	"context"
	"fmt"

	ddService "github.com/LumeraProtocol/dd-service"
	"google.golang.org/grpc"
)

type Client struct {
	conn      *grpc.ClientConn
	ddService ddService.DupeDetectionServerClient
}

type Service interface {
	ImageRarenessScore(ctx context.Context, req RarenessScoreRequest) (ImageRarenessScoreResponse, error)
	GetStatus(ctx context.Context, req GetStatusRequest) (GetStatusResponse, error)
}

func NewClient(serverAddr string) (Service, error) {
	conn, err := grpc.Dial(serverAddr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &Client{
		conn:      conn,
		ddService: ddService.NewDupeDetectionServerClient(conn),
	}, nil
}

func (c *Client) Close() {
	c.conn.Close()
}
