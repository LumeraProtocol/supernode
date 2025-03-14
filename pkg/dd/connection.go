package dd

import (
	"google.golang.org/grpc"
)

// clientConn represents grpc client conneciton.
type clientConn struct {
	*grpc.ClientConn

	id string
}

func (conn *clientConn) DDService(config *Config) DDService {
	return newDDServerClient(conn, config)
}

func newClientConn(id string, conn *grpc.ClientConn) *clientConn {
	return &clientConn{
		ClientConn: conn,
		id:         id,
	}
}
