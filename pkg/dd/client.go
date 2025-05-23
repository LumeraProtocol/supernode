package dd

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/random"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"
)

const (
	defaultConnectTimeout = 60 * time.Second
)

type client struct{}

// Connect implements node.Client.Connect()
func (cl *client) Connect(ctx context.Context, address string) (Connection, error) {
	// Limits the dial timeout, prevent got stuck too long
	dialCtx, cancel := context.WithTimeout(ctx, defaultConnectTimeout)
	defer cancel()

	id, _ := random.String(8, random.Base62Chars)

	grpcConn, err := grpc.DialContext(dialCtx, address,
		//lint:ignore SA1019 we want to ignore this for now
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name), grpc.MaxCallRecvMsgSize(35000000)),
	)
	if err != nil {
		return nil, errors.Errorf("fail to dial: %w", err).WithField("address", address)
	}

	log.DD().WithContext(ctx).Debugf("Connected to %s with max recv size 35 MB", address)

	conn := newClientConn(id, grpcConn)
	go func() {
		//<-conn.Done() // FIXME: to be implemented by new gRPC package
		log.DD().WithContext(ctx).Debugf("Disconnected %s", grpcConn.Target())
	}()
	return conn, nil
}
