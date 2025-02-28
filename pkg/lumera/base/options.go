package base

import (
	"github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// Option defines the functional option signature
type Option func(c *Client)

func (c *Client) WithOptions(opts ...Option) {
	for _, opt := range opts {
		opt(c)
	}
}

func (c *Client) WithTxClient(conn *grpc.ClientConn) Option {
	return func(c *Client) {
		c.txClient = tx.NewServiceClient(conn)
	}
}
