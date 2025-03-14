package raptorq

import (
	"google.golang.org/grpc"
)

// clientConn represents grpc client conneciton.
type clientConn struct {
	*grpc.ClientConn

	id string
}

func (conn *clientConn) RaptorQ(config *Config) RaptorQ {
	return newRaptorQServerClient(conn, config)
}

func newClientConn(id string, conn *grpc.ClientConn) Connection {
	return &clientConn{
		ClientConn: conn,
		id:         id,
	}
}
