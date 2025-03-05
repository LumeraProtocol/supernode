//go:generate mockgen -destination=rq_mock.go -package=raptorq -source=client.go

package raptorq

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	rq "github.com/LumeraProtocol/rq-service"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
)

const (
	concurrency = 1
)

type Client struct {
	conn   *grpc.ClientConn
	config Config

	rqService    rq.RaptorQClient
	lumeraClient *lumera.Client
	semaphore    chan struct{} // Semaphore to control concurrency
}

type Service interface {
	Encode(ctx context.Context, req EncodeRequest) (EncodeResponse, error)
	Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error)
	EncodeMetaData(ctx context.Context, req EncodeMetadataRequest) (EncodeResponse, error)
}

func NewClient(serverAddr string, conf Config, lumeraC *lumera.Client) (Service, error) {
	conn, err := grpc.Dial(serverAddr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &Client{
		conn:         conn,
		rqService:    rq.NewRaptorQClient(conn),
		config:       conf,
		lumeraClient: lumeraC,
		semaphore:    make(chan struct{}, concurrency),
	}, nil
}

func (c *Client) Close() {
	c.conn.Close()
}
