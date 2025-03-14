package raptorq

import (
	"time"

	rq "github.com/LumeraProtocol/supernode/gen/raptorq"
)

const (
	logPrefix             = "grpc-raptorqClient"
	defaultConnectTimeout = 120 * time.Second
)

type raptorQServerClient struct {
	config    *Config
	conn      *clientConn
	rqService rq.RaptorQClient
	semaphore chan struct{} // Semaphore to control concurrency
}

func newRaptorQServerClient(conn *clientConn, config *Config) RaptorQ {
	return &raptorQServerClient{
		conn:      conn,
		rqService: rq.NewRaptorQClient(conn),
		config:    config,
		semaphore: make(chan struct{}, concurrency),
	}
}

func (c *raptorQServerClient) Close() {
	c.conn.Close()
}
