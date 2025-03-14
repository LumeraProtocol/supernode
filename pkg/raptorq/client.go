package raptorq

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/random"

	"google.golang.org/grpc"
)

type client struct{}

// Connect implements node.Client.Connect()
func (client *client) Connect(ctx context.Context, address string) (Connection, error) {
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
	go func() {
		//<-conn.Done() // FIXME: to be implemented by new gRPC package
		log.WithContext(ctx).Debugf("Disconnected RQ %s", grpcConn.Target())
	}()
	return conn, nil
}

// NewClient returns a new client instance.
func NewClient() ClientInterface {
	return &client{}
}
