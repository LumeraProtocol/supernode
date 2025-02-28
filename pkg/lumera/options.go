package lumera

import (
	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
)

// Example usage:
// 
// client, err := NewTendermintClient(
//     WithKeyring(myKeyring),
//     WithTxClient(myConnection),
// )

// Option defines the functional option signature
type Option func(c *Client)

func (c *Client) WithOptions(opts ...Option) {
	for _, opt := range opts {
		opt(c)
	}
}

func WithTxClient(conn *grpc.ClientConn) Option {
	return func(c *Client) {
		c.txClient = tx.NewServiceClient(conn)
	}
}

func WithKeyring(kr keyring.Keyring) Option {
	return func(c *Client) {
		c.cosmosSdk.Keyring = kr
	}
}

