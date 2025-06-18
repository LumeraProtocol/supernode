package dd

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
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

	logtrace.Debug(ctx, "Connected to address with max recv size 35 MB", logtrace.Fields{logtrace.FieldModule: "dd", "address": address})

	conn := newClientConn(id, grpcConn)
	go func() {
		//<-conn.Done() // FIXME: to be implemented by new gRPC package
		logtrace.Debug(ctx, "Disconnected", logtrace.Fields{logtrace.FieldModule: "dd", "target": grpcConn.Target()})
	}()
	return conn, nil
}
