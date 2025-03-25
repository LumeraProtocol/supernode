package client

import (
	"google.golang.org/grpc"

	"github.com/LumeraProtocol/supernode/supernode/node/supernode"
)

// clientConn represents grpc client connection.
type clientConn struct {
	*grpc.ClientConn

	id string
}

func newClientConn(id string, conn *grpc.ClientConn) supernode.ConnectionInterface {
	return &clientConn{
		ClientConn: conn,
		id:         id,
	}
}
