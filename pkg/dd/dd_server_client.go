package dd

import (
	dd "github.com/LumeraProtocol/supernode/gen/dupedetection"
)

type ddServerClientImpl struct {
	config    *Config
	conn      *clientConn
	ddService dd.DupeDetectionServerClient
}

// NewDDServerClient returns a new dd-server-client instance.
func newDDServerClient(conn *clientConn, c *Config) DDService {
	return &ddServerClientImpl{
		config:    c,
		conn:      conn,
		ddService: dd.NewDupeDetectionServerClient(conn),
	}
}

func (c *ddServerClientImpl) Close() {
	c.conn.Close()
}
