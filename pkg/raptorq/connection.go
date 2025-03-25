package raptorq

import (
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"google.golang.org/grpc"
)

// clientConn represents grpc client conneciton.
type clientConn struct {
	*grpc.ClientConn

	id string
}

func (conn *clientConn) RaptorQ(config *Config, lc lumera.Client, store rqstore.Store) RaptorQ {
	return NewRaptorQServerClient(conn, config, lc, store)
}

func newClientConn(id string, conn *grpc.ClientConn) Connection {
	return &clientConn{
		ClientConn: conn,
		id:         id,
	}
}
