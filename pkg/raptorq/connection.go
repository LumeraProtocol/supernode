package raptorq

import (
	"context"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/random"
	"google.golang.org/grpc"
)

const (
	logPrefix             = "hermes-grpc"
	defaultConnectTimeout = 45 * time.Second
)

// clientConn represents grpc client connection.
type clientConn struct {
	*grpc.ClientConn

	id string
}

func (conn *clientConn) RaptorQ(config *Config) Service {
	return conn.newRaptorQ(*config)
}

func newClientConn(id string, conn *grpc.ClientConn) Connection {
	return &clientConn{
		ClientConn: conn,
		id:         id,
	}
}

// ClientInterface represents a base connection interface.
type ClientInterface interface {
	// Connect connects to the server at the given address.
	Connect(ctx context.Context, address string) (Connection, error)
}

// Connection represents a client connection
type Connection interface {
	// Close closes connection.
	Close() error

	// Done returns a channel that's closed when connection is shutdown.
	// Done() <-chan struct{}

	// RaptorQ returns a new RaptorQ stream.
	RaptorQ(config *Config) Service
}

// Connect implements node.Client.Connect()
func (client *Client) Connect(ctx context.Context, address string) (Connection, error) {
	// Limits the dial timeout, prevent got stuck too long
	dialCtx, cancel := context.WithTimeout(ctx, defaultConnectTimeout)
	defer cancel()

	id, _ := random.String(8, random.Base62Chars)
	ctx = log.ContextWithPrefix(ctx, fmt.Sprintf("%s-%s", logPrefix, id))

	grpcConn, err := grpc.DialContext(dialCtx, address,
		//lint:ignore SA1019 we want to ignore this for now
		grpc.WithInsecure(),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, errors.Errorf("fail to dial: %w", err).WithField("address", address)
	}
	log.WithContext(ctx).Debugf("Connected to RQ %s", address)

	conn := newClientConn(id, grpcConn)
	// go func() {
	// 	<-conn.Done()
	// 	log.WithContext(ctx).Debugf("Disconnected RQ %s", grpcConn.Target())
	// }()
	return conn, nil
}
