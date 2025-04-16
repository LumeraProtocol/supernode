package raptorq

import (
	"time"

	rq "github.com/LumeraProtocol/supernode/gen/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
)

const (
	logPrefix             = "grpc-raptorqClient"
	defaultConnectTimeout = 120 * time.Second
)

type raptorQServerClient struct {
	config       *Config
	conn         *clientConn
	rqService    rq.RaptorQClient
	lumeraClient lumera.Client
	semaphore    chan struct{} // Semaphore to control concurrency
}

func NewRaptorQServerClient(conn *clientConn, config *Config, lc lumera.Client) RaptorQ {
	return &raptorQServerClient{
		conn:         conn,
		rqService:    rq.NewRaptorQClient(conn),
		lumeraClient: lc,
		config:       config,
		semaphore:    make(chan struct{}, concurrency),
	}
}

func (c *raptorQServerClient) Close() {
	c.conn.Close()
}
