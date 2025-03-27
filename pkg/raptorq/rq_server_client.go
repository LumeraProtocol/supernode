package raptorq

import (
	"time"

	rq "github.com/LumeraProtocol/supernode/gen/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
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
	store        rqstore.Store
	semaphore    chan struct{} // Semaphore to control concurrency
}

func NewRaptorQServerClient(conn *clientConn,
	config *Config,
	lc lumera.Client,
	store rqstore.Store) RaptorQ {
	return &raptorQServerClient{
		conn:         conn,
		rqService:    rq.NewRaptorQClient(conn),
		lumeraClient: lc,
		store:        store,
		config:       config,
		semaphore:    make(chan struct{}, concurrency),
	}
}

func (c *raptorQServerClient) Close() {
	c.conn.Close()
}
