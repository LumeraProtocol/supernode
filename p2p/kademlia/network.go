package kademlia

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/btcsuite/btcutil/base58"
	json "github.com/json-iterator/go"

	"github.com/LumeraProtocol/supernode/v2/pkg/utils"

	"github.com/google/uuid"
	"go.uber.org/ratelimit"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/credentials"
)

const (
	defaultConnRate                    = 1000
	defaultMaxPayloadSize              = 200 // MB
	errorBusy                          = "Busy"
	maxConcurrentFindBatchValsRequests = 25
	defaultExecTimeout                 = 10 * time.Second
)

// Global map for message type timeouts
var execTimeouts map[int]time.Duration

func init() {
	// Initialize the request execution timeout values
	execTimeouts = map[int]time.Duration{
		Ping:            5 * time.Second,
		FindNode:        10 * time.Second,
		BatchFindNode:   5 * time.Second, // small requests, quick
		FindValue:       5 * time.Second,
		BatchFindValues: 60 * time.Second, // responder compresses
		BatchGetValues:  75 * time.Second, // large, sometimes cloud fetch then send back
		StoreData:       10 * time.Second,
		BatchStoreData:  75 * time.Second, // large uncompressed payloads
		Replicate:       90 * time.Second,
	}
}

// Network for distributed hash table
type Network struct {
	dht      *DHT              // the distributed hash table
	listener net.Listener      // the server socket for the network
	self     *Node             // queries node itself
	limiter  ratelimit.Limiter // the rate limit for accept socket
	done     chan struct{}     // network is stopped

	// For secure connection
	clientTC    credentials.TransportCredentials // outgoing
	serverTC    credentials.TransportCredentials // incoming
	connPool    *ConnPool
	connPoolMtx sync.Mutex
	sem         *semaphore.Weighted

	metrics sync.Map

	// recent request tracking (last 10 entries overall and per IP)
	recentMu              sync.Mutex
	recentStoreOverall    []RecentBatchStoreEntry
	recentStoreByIP       map[string][]RecentBatchStoreEntry
	recentRetrieveOverall []RecentBatchRetrieveEntry
	recentRetrieveByIP    map[string][]RecentBatchRetrieveEntry
}

// NewNetwork returns a network service
func NewNetwork(ctx context.Context, dht *DHT, self *Node, clientTC, serverTC credentials.TransportCredentials) (*Network, error) {
	s := &Network{
		dht:      dht,
		self:     self,
		done:     make(chan struct{}),
		clientTC: clientTC,
		serverTC: serverTC,
		connPool: NewConnPool(ctx),
		sem:      semaphore.NewWeighted(maxConcurrentFindBatchValsRequests),
		metrics:  sync.Map{},
	}
	// init the rate limiter
	s.limiter = ratelimit.New(defaultConnRate)
	s.connPool.StartPruner(ctx, 10*time.Minute, 1*time.Hour)

	addr := fmt.Sprintf("%s:%d", self.IP, self.Port)
	// new tcp listener
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logtrace.Debug(ctx, "Error trying to get tcp socket", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
		})
		return nil, err
	}
	s.listener = listener
	logtrace.Debug(ctx, "Listening on", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"address":            addr,
	})

	return s, nil
}

// Start the network
func (s *Network) Start(ctx context.Context) error {
	// serve the incoming connection
	go s.serve(ctx)

	return nil
}

// Stop the network
func (s *Network) Stop(ctx context.Context) {
	if s.clientTC != nil || s.serverTC != nil {
		s.connPool.Release()
	}
	// close the socket
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			logtrace.Error(ctx, "Close socket failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
		}
	}
}

func (s *Network) encodeMesage(mesage *Message) ([]byte, error) {
	// send the response to client
	encoded, err := encode(mesage)
	if err != nil {
		return nil, errors.Errorf("encode response: %w", err)
	}

	return encoded, nil
}

func (s *Network) handleFindNode(ctx context.Context, message *Message) (res []byte, err error) {
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, FindNode); response != nil || err != nil {
			res = response
		}
	}()

	request, ok := message.Data.(*FindNodeRequest)
	if !ok {
		err := errors.New("invalid FindNodeRequest")
		response := &FindNodeResponse{
			Status: ResponseStatus{
				Result: ResultFailed,
				ErrMsg: err.Error(),
			},
		}
		// new a response message
		resMsg := s.dht.newMessage(FindNode, message.Sender, response)
		return s.encodeMesage(resMsg)
	}

	// add the sender to queries hash table
	s.dht.addNode(ctx, message.Sender)

	// the closest contacts
	hashedTargetID, _ := utils.Blake3Hash(request.Target)
	closest, _ := s.dht.ht.closestContacts(K, hashedTargetID, []*Node{message.Sender})

	response := &FindNodeResponse{
		Status: ResponseStatus{
			Result: ResultOk,
		},
		Closest: closest.Nodes,
	}

	// new a response message
	resMsg := s.dht.newMessage(FindNode, message.Sender, response)
	return s.encodeMesage(resMsg)
}

func (s *Network) handleFindValue(ctx context.Context, message *Message) (res []byte, err error) {
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, FindValue); response != nil || err != nil {
			res = response
		}
	}()

	request, ok := message.Data.(*FindValueRequest)
	if !ok {
		err := errors.New("invalid FindValueRequest")
		return s.generateResponseMessage(FindValue, message.Sender, ResultFailed, err.Error())
	}

	// add the sender to queries hash table
	s.dht.addNode(ctx, message.Sender)

	// retrieve the value from queries storage
	value, err := s.dht.store.Retrieve(ctx, request.Target)
	if err != nil {
		err = errors.Errorf("store retrieve: %w", err)
		response := &FindValueResponse{
			Status: ResponseStatus{
				Result: ResultFailed,
				ErrMsg: err.Error(),
			},
		}

		closest, _ := s.dht.ht.closestContacts(K, request.Target, []*Node{message.Sender})
		response.Closest = closest.Nodes

		// new a response message
		resMsg := s.dht.newMessage(FindValue, message.Sender, response)
		return s.encodeMesage(resMsg)
	}

	response := &FindValueResponse{
		Status: ResponseStatus{
			Result: ResultOk,
		},
	}

	if len(value) > 0 {
		// return the value
		response.Value = value
	} else {
		// return the closest contacts
		closest, _ := s.dht.ht.closestContacts(K, request.Target, []*Node{message.Sender})
		response.Closest = closest.Nodes
	}

	// new a response message
	resMsg := s.dht.newMessage(FindValue, message.Sender, response)
	return s.encodeMesage(resMsg)
}

func (s *Network) handleStoreData(ctx context.Context, message *Message) (res []byte, err error) {
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, StoreData); response != nil || err != nil {
			res = response
		}
	}()

	request, ok := message.Data.(*StoreDataRequest)
	if !ok {
		err := errors.New("invalid StoreDataRequest")
		return s.generateResponseMessage(StoreData, message.Sender, ResultFailed, err.Error())
	}

	logtrace.Debug(ctx, "Handle store data", logtrace.Fields{logtrace.FieldModule: "p2p", "message": message.String()})

	// add the sender to queries hash table
	s.dht.addNode(ctx, message.Sender)

	// format the key
	key, _ := utils.Blake3Hash(request.Data)

	value, err := s.dht.store.Retrieve(ctx, key)
	if err != nil || len(value) == 0 {
		// store the data to queries storage
		if err := s.dht.store.Store(ctx, key, request.Data, request.Type, false); err != nil {
			err = errors.Errorf("store the data: %w", err)
			return s.generateResponseMessage(StoreData, message.Sender, ResultFailed, err.Error())
		}
	}

	response := &StoreDataResponse{
		Status: ResponseStatus{
			Result: ResultOk,
		},
	}

	// new a response message
	resMsg := s.dht.newMessage(StoreData, message.Sender, response)
	return s.encodeMesage(resMsg)
}

func (s *Network) handleReplicate(ctx context.Context, message *Message) (res []byte, err error) {
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, Replicate); response != nil || err != nil {
			res = response
		}
	}()

	request, ok := message.Data.(*ReplicateDataRequest)
	if !ok {
		err := errors.New("invalid ReplicateDataRequest")
		return s.generateResponseMessage(Replicate, message.Sender, ResultFailed, err.Error())
	}

	logtrace.Debug(ctx, "Handle replicate data", logtrace.Fields{logtrace.FieldModule: "p2p", "message": message.String()})

	if err := s.handleReplicateRequest(ctx, request, message.Sender.ID, message.Sender.IP, message.Sender.Port); err != nil {
		return s.generateResponseMessage(Replicate, message.Sender, ResultFailed, err.Error())
	}

	response := &ReplicateDataResponse{
		Status: ResponseStatus{
			Result: ResultOk,
		},
	}

	// new a response message
	resMsg := s.dht.newMessage(Replicate, message.Sender, response)
	return s.encodeMesage(resMsg)
}

func (s *Network) handleReplicateRequest(ctx context.Context, req *ReplicateDataRequest, id []byte, ip string, port uint16) error {
	keysToStore, err := s.dht.store.RetrieveBatchNotExist(ctx, req.Keys, 5000)
	if err != nil {
		logtrace.Error(ctx, "Unable to retrieve batch replication keys", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
			"keys":               len(req.Keys),
			"from-ip":            ip,
		})
		return fmt.Errorf("unable to retrieve batch replication keys: %w", err)
	}

	logtrace.Debug(ctx, "Store batch replication keys to be stored", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"to-store-keys":      len(keysToStore),
		"rcvd-keys":          len(req.Keys),
		"from-ip":            ip,
	})

	if len(keysToStore) > 0 {
		if err := s.dht.store.StoreBatchRepKeys(keysToStore, string(id), ip, port); err != nil {
			return fmt.Errorf("unable to store batch replication keys: %w", err)
		}

		logtrace.Info(ctx, "Store batch replication keys stored", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"to-store-keys":      len(keysToStore),
			"rcvd-keys":          len(req.Keys),
			"from-ip":            ip,
		})
	}

	return nil
}

func (s *Network) handlePing(_ context.Context, message *Message) ([]byte, error) {
	// new a response message
	resMsg := s.dht.newMessage(Ping, message.Sender, nil)

	go s.dht.addNode(context.Background(), message.Sender)

	return s.encodeMesage(resMsg)
}

// handle the connection request
func (s *Network) handleConn(ctx context.Context, rawConn net.Conn) {
	var conn net.Conn
	var err error
	logtrace.Debug(ctx, "Handle connection", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"local-addr":         rawConn.LocalAddr().String(),
		"remote-addr":        rawConn.RemoteAddr().String(),
	})
	// secure handshake
	if s.serverTC != nil {
		conn, err = NewSecureServerConn(ctx, s.serverTC, rawConn)
		if err != nil {
			_ = rawConn.Close()
			logtrace.Warn(ctx, "Server secure handshake failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
			return
		}
	} else {
		conn = rawConn
	}
	defer conn.Close()

	const serverReadTimeout = 90 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// per-message read deadline
		_ = conn.SetReadDeadline(time.Now().Add(serverReadTimeout))

		// read request
		request, err := decode(conn)
		if err != nil {
			if err == io.EOF {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				logtrace.Debug(ctx, "Read and decode timed out, keeping connection open", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				continue
			}
			logtrace.Warn(ctx, "Read and decode failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
			return
		}
		reqID := uuid.New().String()
		mt := request.MessageType

		// invoke handler with metrics
		var response []byte
		var hErr error

		switch mt {
		case FindNode:
			response, hErr = s.withMetrics(FindNode, func() ([]byte, error) {
				return s.handleFindNode(ctx, request)
			})
		case BatchFindNode:
			response, hErr = s.withMetrics(BatchFindNode, func() ([]byte, error) {
				return s.handleBatchFindNode(ctx, request)
			})
		case FindValue:
			response, hErr = s.withMetrics(FindValue, func() ([]byte, error) {
				return s.handleFindValue(ctx, request)
			})
		case Ping:
			response, hErr = s.withMetrics(Ping, func() ([]byte, error) {
				return s.handlePing(ctx, request)
			})
		case StoreData:
			response, hErr = s.withMetrics(StoreData, func() ([]byte, error) {
				return s.handleStoreData(ctx, request)
			})
		case Replicate:
			response, hErr = s.withMetrics(Replicate, func() ([]byte, error) {
				return s.handleReplicate(ctx, request)
			})
		case BatchFindValues:
			response, hErr = s.withMetrics(BatchFindValues, func() ([]byte, error) {
				return s.handleBatchFindValues(ctx, request, reqID)
			})
		case BatchStoreData:
			response, hErr = s.withMetrics(BatchStoreData, func() ([]byte, error) {
				return s.handleBatchStoreData(ctx, request)
			})
		case BatchGetValues:
			response, hErr = s.withMetrics(BatchGetValues, func() ([]byte, error) {
				return s.handleGetValuesRequest(ctx, request, reqID)
			})
		default:
			// count unknown types as failure and return
			m := s.metricsFor(mt)
			m.Total.Add(1)
			m.Failure.Add(1)
			logtrace.Error(ctx, "Invalid message type", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"message-type":       mt,
			})
			return
		}

		if hErr != nil {
			// handler already logged; keep connection open for next request
			continue
		}

		// write the response (transport write failures counted as well)
		_ = conn.SetWriteDeadline(time.Now().Add(serverReadTimeout))
		if _, err := conn.Write(response); err != nil {
			s.markTransportWrite(mt, err)
			logtrace.Error(ctx, "Write failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
				"p2p-req-id":         reqID,
				"message-type":       mt,
			})
			return
		}
	}
}

// isTemporaryNetError checks if the error is a known temporary network error
func isTemporaryNetError(err error) bool {
	// Check for specific error types that are typically temporary
	switch err {
	case syscall.EAGAIN, syscall.ECONNABORTED, syscall.ECONNRESET, syscall.ECONNREFUSED,
		syscall.EINTR, syscall.ETIMEDOUT:
		return true
	}

	// Some network errors might be wrapped in other errors
	// Check for syscall errors specifically
	var sysErr syscall.Errno
	if errors.As(err, &sysErr) {
		switch sysErr {
		case syscall.EAGAIN, syscall.ECONNABORTED, syscall.ECONNRESET, syscall.ECONNREFUSED,
			syscall.EINTR, syscall.ETIMEDOUT:
			return true
		}
	}

	return false
}

// serve the incomming connection
func (s *Network) serve(ctx context.Context) {
	var tempDelay time.Duration // how long to sleep on accept failure

	for {
		// rate limiter for the incomming connections
		s.limiter.Take()

		// accept the incomming connections
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Handle specific known network errors that are generally temporary
			if isTemporaryNetError(err) {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				logtrace.Warn(ctx, "Socket accept failed, retrying", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
					"retry-in":           tempDelay.String(),
				})

				time.Sleep(tempDelay)
				continue
			}

			logtrace.Error(ctx, "Socket accept failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
			return
		}

		// handle the connection requests
		go s.handleConn(ctx, conn)
	}
}

// getExecTimeout returns the timeout for the given message type
func getExecTimeout(messageType int, isLong bool) time.Duration {
	if isLong {
		return 3 * time.Minute
	}
	if timeout, exists := execTimeouts[messageType]; exists {
		return timeout
	}
	return defaultExecTimeout
}

// Call sends the request to target and receive the response
func (s *Network) Call(ctx context.Context, request *Message, isLong bool) (*Message, error) {
	timeout := getExecTimeout(request.MessageType, isLong)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if request.Receiver != nil && request.Receiver.Port == 50052 {
		logtrace.Error(ctx, "Invalid receiver port", logtrace.Fields{logtrace.FieldModule: "p2p"})
		return nil, errors.New("invalid receiver port")
	}
	if request.Sender != nil && request.Sender.Port == 50052 {
		logtrace.Error(ctx, "Invalid sender port", logtrace.Fields{logtrace.FieldModule: "p2p"})
		return nil, errors.New("invalid sender port")
	}
	if s.clientTC == nil {
		return nil, errors.New("secure transport credentials are not set")
	}

	// pool key: bech32@ip:port (bech32 identity is your invariant)
	idStr := string(request.Receiver.ID)
	remoteAddr := fmt.Sprintf("%s@%s:%d", idStr, strings.TrimSpace(request.Receiver.IP), request.Receiver.Port)

	// try get from pool
	s.connPoolMtx.Lock()
	conn, err := s.connPool.Get(remoteAddr)
	s.connPoolMtx.Unlock()

	// miss: dial, then double-check add
	if err != nil {
		newConn, dialErr := NewSecureClientConn(ctx, s.clientTC, remoteAddr)
		if dialErr != nil {
			return nil, errors.Errorf("client secure establish %q: %w", remoteAddr, dialErr)
		}
		s.connPoolMtx.Lock()
		if existing, getErr := s.connPool.Get(remoteAddr); getErr == nil {
			_ = newConn.Close()
			conn = existing
		} else {
			s.connPool.Add(remoteAddr, newConn)
			conn = newConn
		}
		s.connPoolMtx.Unlock()
	}

	// Encode once
	data, err := encode(request)
	if err != nil {
		return nil, errors.Errorf("encode: %w", err)
	}

	// Wrapper: lock whole RPC to prevent cross-talk; retry once on stale pooled socket
	if cw, ok := conn.(*connWrapper); ok {
		return s.rpcOnceWrapper(ctx, cw, remoteAddr, data, timeout, request.MessageType)
	}

	// Non-wrapper fallback: one stale retry
	return s.rpcOnceNonWrapper(ctx, conn, remoteAddr, data, timeout, request.MessageType)
}

// ---- retryable RPC helpers -------------------------------------------------

func (s *Network) rpcOnceWrapper(ctx context.Context, cw *connWrapper, remoteAddr string, data []byte, timeout time.Duration, msgType int) (*Message, error) {
	writeDL := calcWriteDeadline(timeout, len(data), 1.0) // target ~1 MB/s

	retried := false
	for {
		// lock the WHOLE RPC on a pooled wrapper
		cw.mtx.Lock()

		// write
		if e := cw.secureConn.SetWriteDeadline(time.Now().Add(writeDL)); e != nil {
			cw.mtx.Unlock()
			s.dropFromPool(remoteAddr, cw)
			return nil, errors.Errorf("set write deadline: %w", e)
		}
		if _, e := cw.secureConn.Write(data); e != nil {
			cw.mtx.Unlock()
			if isStaleConnError(e) && !retried {
				logtrace.Info(ctx, "Stale pooled connection on write; redialing", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"remote":             remoteAddr,
					"message_type":       msgType,
				})
				s.dropFromPool(remoteAddr, cw)
				fresh, derr := NewSecureClientConn(ctx, s.clientTC, remoteAddr)
				if derr != nil {
					logtrace.Error(ctx, "Retry redial failed (write)", logtrace.Fields{
						logtrace.FieldModule: "p2p",
						"remote":             remoteAddr,
						"message_type":       msgType,
						logtrace.FieldError:  derr.Error(),
					})
					return nil, errors.Errorf("re-dial after write: %w", derr)
				}
				s.addToPool(remoteAddr, fresh)
				if nw, ok := fresh.(*connWrapper); ok {
					cw = nw
					retried = true
					continue // retry whole RPC under the new wrapper
				}
				// Non-wrapper fallback retry
				return s.rpcOnceNonWrapper(ctx, fresh, remoteAddr, data, timeout, msgType)
			}
			s.dropFromPool(remoteAddr, cw)
			return nil, errors.Errorf("conn write: %w", e)
		}

		// read
		rdl := readDeadlineFor(msgType, timeout)
		if e := cw.secureConn.SetReadDeadline(time.Now().Add(rdl)); e != nil {
			cw.mtx.Unlock()
			s.dropFromPool(remoteAddr, cw)
			return nil, errors.Errorf("set read deadline: %w", e)
		}
		r, e := decode(cw.secureConn)
		_ = cw.secureConn.SetDeadline(time.Time{})
		cw.mtx.Unlock()
		if e != nil {
			if isStaleConnError(e) && !retried {
				logtrace.Info(ctx, "Stale pooled connection on read; redialing", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"remote":             remoteAddr,
					"message_type":       msgType,
				})
				s.dropFromPool(remoteAddr, cw)
				fresh, derr := NewSecureClientConn(ctx, s.clientTC, remoteAddr)
				if derr != nil {
					logtrace.Error(ctx, "Retry redial failed (read)", logtrace.Fields{
						logtrace.FieldModule: "p2p",
						"remote":             remoteAddr,
						"message_type":       msgType,
						logtrace.FieldError:  derr.Error(),
					})
					return nil, errors.Errorf("re-dial after read: %w", derr)
				}
				s.addToPool(remoteAddr, fresh)
				if nw, ok := fresh.(*connWrapper); ok {
					cw = nw
					retried = true
					continue // retry whole RPC
				}
				return s.rpcOnceNonWrapper(ctx, fresh, remoteAddr, data, timeout, msgType)
			}
			s.dropFromPool(remoteAddr, cw)
			return nil, errors.Errorf("conn read: %w", e)
		}
		return r, nil
	}
}

func (s *Network) rpcOnceNonWrapper(ctx context.Context, conn net.Conn, remoteAddr string, data []byte, timeout time.Duration, msgType int) (*Message, error) {
	sizeMB := float64(len(data)) / (1024.0 * 1024.0) // data is your gob-encoded message
	throughputFloor := 8.0                           // MB/s (~64 Mbps)
	est := time.Duration(sizeMB / throughputFloor * float64(time.Second))
	base := 1 * time.Second
	cushion := 5 * time.Second

	writeDL := base + est + cushion
	if writeDL < 5*time.Second {
		writeDL = 5 * time.Second
	}
	if writeDL > timeout-1*time.Second {
		writeDL = timeout - 1*time.Second
	}
	retried := false
Retry:
	if err := conn.SetWriteDeadline(time.Now().Add(writeDL)); err != nil {
		s.dropFromPool(remoteAddr, conn)
		return nil, errors.Errorf("set write deadline: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		if isStaleConnError(err) && !retried {
			logtrace.Info(ctx, "Stale pooled connection on write; redialing", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"remote":             remoteAddr,
				"message_type":       msgType,
			})
			s.dropFromPool(remoteAddr, conn)
			fresh, derr := NewSecureClientConn(ctx, s.clientTC, remoteAddr)
			if derr != nil {
				logtrace.Error(ctx, "Retry redial failed (write)", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"remote":             remoteAddr,
					"message_type":       msgType,
					logtrace.FieldError:  derr.Error(),
				})
				return nil, errors.Errorf("re-dial after write: %w", derr)
			}
			s.addToPool(remoteAddr, fresh)
			conn = fresh
			retried = true
			goto Retry
		}
		s.dropFromPool(remoteAddr, conn)
		return nil, errors.Errorf("conn write: %w", err)
	}

	rdl := readDeadlineFor(msgType, timeout)
	if err := conn.SetReadDeadline(time.Now().Add(rdl)); err != nil {
		s.dropFromPool(remoteAddr, conn)
		return nil, errors.Errorf("set read deadline: %w", err)
	}
	resp, err := decode(conn)
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		if isStaleConnError(err) && !retried {
			logtrace.Info(ctx, "Stale pooled connection on read; redialing", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"remote":             remoteAddr,
				"message_type":       msgType,
			})
			s.dropFromPool(remoteAddr, conn)
			fresh, derr := NewSecureClientConn(ctx, s.clientTC, remoteAddr)
			if derr != nil {
				logtrace.Error(ctx, "Retry redial failed (read)", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"remote":             remoteAddr,
					"message_type":       msgType,
					logtrace.FieldError:  derr.Error(),
				})
				return nil, errors.Errorf("re-dial after read: %w", derr)
			}
			s.addToPool(remoteAddr, fresh)
			conn = fresh
			retried = true
			goto Retry
		}
		s.dropFromPool(remoteAddr, conn)
		return nil, errors.Errorf("conn read: %w", err)
	}
	return resp, nil
}

// classify stale pooled sockets (not timeouts)
func isStaleConnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "use of closed network connection"),
		strings.Contains(s, "connection reset by peer"),
		strings.Contains(s, "broken pipe"),
		strings.Contains(s, "connection aborted"):
		return true
	default:
		return false
	}
}

// small helpers for pool ops
func (s *Network) dropFromPool(key string, c net.Conn) {
	s.connPoolMtx.Lock()
	_ = c.Close()
	s.connPool.Del(key)
	s.connPoolMtx.Unlock()
}
func (s *Network) addToPool(key string, c net.Conn) {
	s.connPoolMtx.Lock()
	s.connPool.Add(key, c)
	s.connPoolMtx.Unlock()
}

func (s *Network) handleBatchFindValues(ctx context.Context, message *Message, reqID string) (res []byte, err error) {
	// Try to acquire the semaphore, wait up to 1 minute
	logtrace.Debug(ctx, "Attempting to acquire semaphore immediately", logtrace.Fields{logtrace.FieldModule: "p2p"})
	if !s.sem.TryAcquire(1) {
		logtrace.Info(ctx, "Immediate acquisition failed. Waiting up to 1 minute", logtrace.Fields{logtrace.FieldModule: "p2p"})
		ctxWithTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
		defer cancel()

		if err := s.sem.Acquire(ctxWithTimeout, 1); err != nil {
			logtrace.Error(ctx, "Failed to acquire semaphore within 1 minute", logtrace.Fields{logtrace.FieldModule: "p2p"})
			// failed to acquire semaphore within 1 minute
			return s.generateResponseMessage(BatchFindValues, message.Sender, ResultFailed, errorBusy)
		}
		logtrace.Info(ctx, "Semaphore acquired after waiting", logtrace.Fields{logtrace.FieldModule: "p2p"})
	}

	// Add a defer function to recover from panic
	defer func() {
		s.sem.Release(1)

		if r := recover(); r != nil {
			// Log the error or handle it as you see fit
			logtrace.Error(ctx, "HandleBatchFindValues recovered from panic", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"panic":              fmt.Sprintf("%v", r),
			})

			// Convert panic to error
			switch t := r.(type) {
			case string:
				err = errors.New(t)
			case error:
				err = t
			default:
				err = errors.New("unknown error")
			}

			res, _ = s.generateResponseMessage(BatchFindValues, message.Sender, ResultFailed, err.Error())
		}
	}()

	request, ok := message.Data.(*BatchFindValuesRequest)
	if !ok {
		return s.generateResponseMessage(BatchFindValues, message.Sender, ResultFailed, "invalid BatchFindValueRequest")
	}

	isDone, data, err := s.handleBatchFindValuesRequest(ctx, request, message.Sender.IP, reqID)
	if err != nil {
		return s.generateResponseMessage(BatchFindValues, message.Sender, ResultFailed, err.Error())
	}

	response := &BatchFindValuesResponse{
		Status: ResponseStatus{
			Result: ResultOk,
		},
		Response: data,
		Done:     isDone,
	}

	resMsg := s.dht.newMessage(BatchFindValues, message.Sender, response)
	return s.encodeMesage(resMsg)
}

func (s *Network) handleGetValuesRequest(ctx context.Context, message *Message, reqID string) (res []byte, err error) {
	start := time.Now()
	appended := false
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, BatchGetValues); response != nil || err != nil {
			res = response
			if !appended {
				s.appendRetrieveEntry(message.Sender.IP, RecentBatchRetrieveEntry{
					TimeUnix:   time.Now().UTC().Unix(),
					SenderID:   string(message.Sender.ID),
					SenderIP:   message.Sender.IP,
					Requested:  0,
					Found:      0,
					DurationMS: time.Since(start).Milliseconds(),
					Error:      "panic/recovered",
				})
			}
		}
	}()

	request, ok := message.Data.(*BatchGetValuesRequest)
	if !ok {
		err := errors.New("invalid BatchGetValuesRequest")
		s.appendRetrieveEntry(message.Sender.IP, RecentBatchRetrieveEntry{
			TimeUnix:   time.Now().UTC().Unix(),
			SenderID:   string(message.Sender.ID),
			SenderIP:   message.Sender.IP,
			Requested:  0,
			Found:      0,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      err.Error(),
		})
		appended = true
		return s.generateResponseMessage(BatchGetValues, message.Sender, ResultFailed, err.Error())
	}

	logtrace.Info(ctx, "Batch get values request received", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"from":               message.Sender.String(),
	})

	// add the sender to queries hash table
	s.dht.addNode(ctx, message.Sender)
	keys := make([]string, len(request.Data))
	i := 0
	for key := range request.Data {
		keys[i] = key
		i++
	}

	values, count, err := s.dht.store.RetrieveBatchValues(ctx, keys, true)
	if err != nil {
		err = errors.Errorf("batch find values: %w", err)
		s.appendRetrieveEntry(message.Sender.IP, RecentBatchRetrieveEntry{
			TimeUnix:   time.Now().UTC().Unix(),
			SenderID:   string(message.Sender.ID),
			SenderIP:   message.Sender.IP,
			Requested:  len(keys),
			Found:      count,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      err.Error(),
		})
		appended = true
		return s.generateResponseMessage(BatchGetValues, message.Sender, ResultFailed, err.Error())
	}

	logtrace.Info(ctx, "Batch get values request processed", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"requested-keys":     len(keys),
		"found":              count,
		"sender":             message.Sender.String(),
	})

	for i, key := range keys {
		val := KeyValWithClosest{
			Value: values[i],
		}
		if len(val.Value) == 0 {
			decodedKey, err := hex.DecodeString(keys[i])
			if err != nil {
				err = errors.Errorf("batch find vals: decode key: %w - key %s", err, keys[i])
				return s.generateResponseMessage(BatchGetValues, message.Sender, ResultFailed, err.Error())
			}

			nodes, _ := s.dht.ht.closestContacts(Alpha, decodedKey, []*Node{message.Sender})
			val.Closest = nodes.Nodes
		}

		request.Data[key] = val
	}

	response := &BatchGetValuesResponse{
		Data: request.Data,
		Status: ResponseStatus{
			Result: ResultOk,
		},
	}

	// new a response message
	resMsg := s.dht.newMessage(BatchGetValues, message.Sender, response)
	s.appendRetrieveEntry(message.Sender.IP, RecentBatchRetrieveEntry{
		TimeUnix:   time.Now().UTC().Unix(),
		SenderID:   string(message.Sender.ID),
		SenderIP:   message.Sender.IP,
		Requested:  len(keys),
		Found:      count,
		DurationMS: time.Since(start).Milliseconds(),
		Error:      "",
	})
	appended = true
	return s.encodeMesage(resMsg)
}

func (s *Network) handleBatchFindValuesRequest(ctx context.Context, req *BatchFindValuesRequest, ip string, reqID string) (isDone bool, compressedData []byte, err error) {
	// log.WithContext(ctx).WithField("p2p-req-id", reqID).WithField("keys", len(req.Keys)).WithField("from-ip", ip).Info("batch find values request received")
	logtrace.Info(ctx, "Batch find values request received", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"from":               ip,
		"keys":               len(req.Keys),
		"p2p-req-id":         reqID,
	})
	if len(req.Keys) > 0 {
		// log.WithContext(ctx).WithField("p2p-req-id", reqID).WithField("keys[0]", req.Keys[0]).WithField("keys[len]", req.Keys[len(req.Keys)-1]).
		// 	WithField("from-ip", ip).Debug("first & last batch keys")
		logtrace.Debug(ctx, "First & last batch keys", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"p2p-req-id":         reqID,
			"keys[0]":            req.Keys[0],
			"keys[len]":          req.Keys[len(req.Keys)-1],
			"from-ip":            ip,
		})
	}

	values, count, err := s.dht.store.RetrieveBatchValues(ctx, req.Keys, true)
	if err != nil {
		return false, nil, fmt.Errorf("failed to retrieve batch values: %w", err)
	}
	// log.WithContext(ctx).WithField("p2p-req-id", reqID).WithField("values-len", len(values)).WithField("found", count).WithField("from-ip", ip).Info("batch find values request processed")
	logtrace.Info(ctx, "Batch find values request processed", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"p2p-req-id":         reqID,
		"values-len":         len(values),
		"found":              count,
		"from-ip":            ip,
	})

	isDone, count, compressedData, err = findOptimalCompression(count, req.Keys, values)
	if err != nil {
		return false, nil, fmt.Errorf("failed to find optimal compression: %w", err)
	}

	// log.WithContext(ctx).WithField("p2p-req-id", reqID).WithField("compressed-data-len", utils.BytesToMB(uint64(len(compressedData)))).WithField("found", count).
	// WithField("from-ip", ip).Info("batch find values response sent")
	logtrace.Info(ctx, "Batch find values response sent", logtrace.Fields{
		logtrace.FieldModule:  "p2p",
		"p2p-req-id":          reqID,
		"compressed-data-len": utils.BytesToMB(uint64(len(compressedData))),
		"found":               count,
		"from-ip":             ip,
	})

	return isDone, compressedData, nil
}

func findOptimalCompression(count int, keys []string, values [][]byte) (bool, int, []byte, error) {
	dataMap := make(map[string][]byte)
	for i, key := range keys {
		dataMap[key] = values[i]
	}

	compressedData, err := compressMap(dataMap)
	if err != nil {
		return true, 0, nil, err
	}

	// If the initial compressed data is under the threshold
	if utils.BytesIntToMB(len(compressedData)) < defaultMaxPayloadSize {
		// log.WithField("compressed-data-len", utils.BytesToMB(uint64(len(compressedData)))).WithField("count", count).Debug("initial compression")
		logtrace.Debug(context.TODO(), "Initial compression", logtrace.Fields{
			"compressed-data-len": utils.BytesToMB(uint64(len(compressedData))),
			"count":               count,
		})
		return true, len(dataMap), compressedData, nil
	}

	iter := 0
	currentValuesCount := count
	for utils.BytesIntToMB(len(compressedData)) >= defaultMaxPayloadSize {
		size := utils.BytesIntToMB(len(compressedData))
		// log.WithField("compressed-data-len", size).WithField("current-count", currentValuesCount).WithField("iter", iter).Debug("optimal compression")
		logtrace.Debug(context.TODO(), "Optimal compression", logtrace.Fields{
			"compressed-data-len": size,
			"current-count":       currentValuesCount,
			"iter":                iter,
		})
		iter++
		// Find top 10 heaviest values and set their keys to nil in the map
		var heavyKeys []string
		currentValuesCount, heavyKeys = findTopHeaviestKeys(dataMap, size)
		for _, key := range heavyKeys {
			dataMap[key] = nil
		}

		// Recompress
		compressedData, err = compressMap(dataMap)
		if err != nil {
			return false, 0, nil, err
		}
	}

	// Calculate the count of non-nil keys
	counter := 0
	for _, v := range dataMap {
		if len(v) > 0 {
			counter++
		}
	}

	// if we were not able to fit even 1 key, there's nothing we can do at this point
	return counter == 0, counter, compressedData, nil
}

func compressMap(dataMap map[string][]byte) ([]byte, error) {
	dataBytes, err := json.Marshal(dataMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data map: %w", err)
	}

	compressedData, err := utils.Compress(dataBytes, 2)
	if err != nil {
		return nil, fmt.Errorf("failed to compress data: %w", err)
	}

	return compressedData, nil
}

func findTopHeaviestKeys(dataMap map[string][]byte, size int) (int, []string) {
	type kv struct {
		Key string
		Len int
	}

	var sorted []kv
	count := 0
	for k, v := range dataMap {
		if len(v) > 0 { // Only consider non-nil values
			count++
			sorted = append(sorted, kv{k, len(v)})
		}
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Len > sorted[j].Len
	})

	n := 10          // number of keys to remove from payload if payload is heavier than allowed size
	if count <= 50 { // if keys are less than 50, we'd wanna try a smaller decrement number
		n = 5
	}
	if count <= 10 { // if keys are less than 10, we'd wanna try a smaller decrement number
		n = 1
	}

	if size > (2 * defaultMaxPayloadSize) {
		// log.Debug("find optimal compression decreasing payload by half")
		logtrace.Debug(context.TODO(), "Find optimal compression decreasing payload by half", logtrace.Fields{
			"size":  size,
			"count": count,
		})
		n = count / 2
	}

	topKeys := []string{}
	for i := 0; i < n && i < len(sorted); i++ {
		topKeys = append(topKeys, sorted[i].Key)
	}

	return count, topKeys
}

func (s *Network) handleBatchStoreData(ctx context.Context, message *Message) (res []byte, err error) {
	start := time.Now()
	appended := false
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, BatchStoreData); response != nil || err != nil {
			res = response
			if !appended {
				s.appendStoreEntry(message.Sender.IP, RecentBatchStoreEntry{
					TimeUnix:   time.Now().UTC().Unix(),
					SenderID:   string(message.Sender.ID),
					SenderIP:   message.Sender.IP,
					Keys:       0,
					DurationMS: time.Since(start).Milliseconds(),
					OK:         false,
					Error:      "panic/recovered",
				})
			}
		}
	}()

	request, ok := message.Data.(*BatchStoreDataRequest)
	if !ok {
		err := errors.New("invalid BatchStoreDataRequest")
		s.appendStoreEntry(message.Sender.IP, RecentBatchStoreEntry{
			TimeUnix:   time.Now().UTC().Unix(),
			SenderID:   string(message.Sender.ID),
			SenderIP:   message.Sender.IP,
			Keys:       0,
			DurationMS: time.Since(start).Milliseconds(),
			OK:         false,
			Error:      err.Error(),
		})
		appended = true
		return s.generateResponseMessage(BatchStoreData, message.Sender, ResultFailed, err.Error())
	}

	// log.P2P().WithContext(ctx).Info("handle batch store data request received")
	logtrace.Info(ctx, "Handle batch store data request received", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"sender":             message.Sender.String(),
		"keys":               len(request.Data),
	})

	// add the sender to queries hash table
	s.dht.addNode(ctx, message.Sender)

	if err := s.dht.store.StoreBatch(ctx, request.Data, 1, false); err != nil {
		err = errors.Errorf("batch store the data: %w", err)
		s.appendStoreEntry(message.Sender.IP, RecentBatchStoreEntry{
			TimeUnix:   time.Now().UTC().Unix(),
			SenderID:   string(message.Sender.ID),
			SenderIP:   message.Sender.IP,
			Keys:       len(request.Data),
			DurationMS: time.Since(start).Milliseconds(),
			OK:         false,
			Error:      err.Error(),
		})
		appended = true
		return s.generateResponseMessage(BatchStoreData, message.Sender, ResultFailed, err.Error())
	}

	response := &StoreDataResponse{
		Status: ResponseStatus{
			Result: ResultOk,
		},
	}
	// log.P2P().WithContext(ctx).Info("handle batch store data request processed")
	logtrace.Info(ctx, "Handle batch store data request processed", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"sender":             message.Sender.String(),
		"keys":               len(request.Data),
	})

	// new a response message
	resMsg := s.dht.newMessage(BatchStoreData, message.Sender, response)
	s.appendStoreEntry(message.Sender.IP, RecentBatchStoreEntry{
		TimeUnix:   time.Now().UTC().Unix(),
		SenderID:   string(message.Sender.ID),
		SenderIP:   message.Sender.IP,
		Keys:       len(request.Data),
		DurationMS: time.Since(start).Milliseconds(),
		OK:         true,
		Error:      "",
	})
	appended = true
	return s.encodeMesage(resMsg)
}

func (s *Network) handleBatchFindNode(ctx context.Context, message *Message) (res []byte, err error) {
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, BatchFindNode); response != nil || err != nil {
			res = response
		}
	}()

	request, ok := message.Data.(*BatchFindNodeRequest)
	if !ok {
		err := errors.New("invalid FindNodeRequest")
		return s.generateResponseMessage(BatchFindNode, message.Sender, ResultFailed, err.Error())
	}

	// add the sender to queries hash table
	s.dht.addNode(ctx, message.Sender)

	response := &BatchFindNodeResponse{
		Status: ResponseStatus{
			Result: ResultOk,
		},
	}
	closestMap := make(map[string][]*Node)

	// log.WithContext(ctx).WithField("sender", message.Sender.String()).Info("Batch Find Nodes Request Received")
	logtrace.Info(ctx, "Batch Find Nodes Request Received", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"sender":             message.Sender.String(),
		"hashed-targets":     len(request.HashedTarget),
	})
	for _, hashedTargetID := range request.HashedTarget {
		closest, _ := s.dht.ht.closestContacts(K, hashedTargetID, []*Node{message.Sender})
		closestMap[base58.Encode(hashedTargetID)] = closest.Nodes
	}
	response.ClosestNodes = closestMap
	// log.WithContext(ctx).WithField("sender", message.Sender.String()).Info("Batch Find Nodes Request Processed")
	logtrace.Info(ctx, "Batch Find Nodes Request Processed", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"sender":             message.Sender.String(),
	})

	// new a response message
	resMsg := s.dht.newMessage(BatchFindNode, message.Sender, response)
	return s.encodeMesage(resMsg)
}

func (s *Network) generateResponseMessage(messageType int, receiver *Node, result ResultType, errMsg string) ([]byte, error) {
	responseStatus := ResponseStatus{
		Result: result,
		ErrMsg: errMsg,
	}

	var response interface{}

	switch messageType {
	case StoreData, BatchStoreData:
		response = &StoreDataResponse{Status: responseStatus}
	case FindNode:
		response = &FindNodeResponse{Status: responseStatus}
	case BatchFindNode:
		response = &BatchFindNodeResponse{Status: responseStatus}
	case FindValue:
		response = &FindValueResponse{Status: responseStatus}
	case BatchFindValues:
		response = &BatchFindValuesResponse{Status: responseStatus}
	case Replicate:
		response = &ReplicateDataResponse{Status: responseStatus}
	case BatchGetValues:
		response = &BatchGetValuesResponse{Status: responseStatus}
	default:
		return nil, fmt.Errorf("unsupported message type %d", messageType)
	}

	resMsg := s.dht.newMessage(messageType, receiver, response)
	return s.encodeMesage(resMsg)
}

func (s *Network) handlePanic(ctx context.Context, sender *Node, messageType int) (res []byte, err error) {
	if r := recover(); r != nil {
		// log.WithContext(ctx).Errorf("p2p network: recovered from panic: %v", r)
		logtrace.Error(ctx, "P2P network: recovered from panic", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"sender":             sender.String(),
			"message-type":       messageType,
		})

		switch t := r.(type) {
		case string:
			err = errors.New(t)
		case error:
			err = t
		default:
			err = errors.New("unknown error")
		}

		if res, err := s.generateResponseMessage(messageType, sender, ResultFailed, err.Error()); err != nil {
			// log.WithContext(ctx).Errorf("Error generating response message: %v", err)
			logtrace.Error(ctx, "Error generating response message", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
		} else {
			return res, err
		}
	}

	return nil, nil
}

func isLocalCancel(err error) bool {
	if err == nil {
		return false
	}
	// our own contexts
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// net timeout also flows as DeadlineExceeded through some stacks – we want
	// checkNodeActivity to decide; for hot path ops we do not count pure timeouts here
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return false
}

// Per-handler counters
type HandleMetrics struct {
	Total   atomic.Int64
	Success atomic.Int64
	Failure atomic.Int64
	Timeout atomic.Int64
}

// Get-or-create metrics bucket for a message type.
func (s *Network) metricsFor(t int) *HandleMetrics {
	if v, ok := s.metrics.Load(t); ok {
		if m, ok := v.(*HandleMetrics); ok && m != nil {
			return m
		}
	}
	m := &HandleMetrics{}
	actual, _ := s.metrics.LoadOrStore(t, m)
	return actual.(*HandleMetrics)
}

// Wrap a handler so we consistently count total / success / failure / timeout.
func (s *Network) withMetrics(t int, fn func() ([]byte, error)) ([]byte, error) {
	m := s.metricsFor(t)
	m.Total.Add(1)

	res, err := fn()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			m.Timeout.Add(1)
		} else {
			m.Failure.Add(1)
		}
		return res, err
	}

	m.Success.Add(1)
	return res, nil
}

// After a successful handler, if the write to the socket fails we also count that.
func (s *Network) markTransportWrite(t int, err error) {
	if err == nil {
		return
	}
	m := s.metricsFor(t)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		m.Timeout.Add(1)
	} else {
		m.Failure.Add(1)
	}
}

// Optional: export a snapshot (e.g., in DHT metrics or /debug handler)
type HandleCounters struct {
	Total   int64 `json:"total"`
	Success int64 `json:"success"`
	Failure int64 `json:"failure"`
	Timeout int64 `json:"timeout"`
}

func msgName(t int) string {
	switch t {
	case Ping:
		return "Ping"
	case StoreData:
		return "StoreData"
	case FindNode:
		return "FindNode"
	case BatchFindNode:
		return "BatchFindNode"
	case FindValue:
		return "FindValue"
	case BatchFindValues:
		return "BatchFindValues"
	case BatchGetValues:
		return "BatchGetValues"
	case BatchStoreData:
		return "BatchStoreData"
	case Replicate:
		return "Replicate"
	default:
		return fmt.Sprintf("Type_%d", t)
	}
}

func (s *Network) HandleMetricsSnapshot() map[string]HandleCounters {
	out := make(map[string]HandleCounters)
	s.metrics.Range(func(k, v any) bool {
		t, _ := k.(int)
		m, _ := v.(*HandleMetrics)
		if m == nil {
			return true
		}
		out[msgName(t)] = HandleCounters{
			Total:   m.Total.Load(),
			Success: m.Success.Load(),
			Failure: m.Failure.Load(),
			Timeout: m.Timeout.Load(),
		}
		return true
	})
	return out
}

// Reuse your calibrated read deadline chooser from client RPC side.
func readDeadlineFor(msgType int, overall time.Duration) time.Duration {
	small := 10 * time.Second
	switch msgType {
	case Ping, FindNode, BatchFindNode, FindValue, StoreData, BatchStoreData:
		if overall > small+1*time.Second {
			return small
		}
		return overall - 1*time.Second
	default:
		return overall // Bulk responses keep full budget
	}
}

// calcWriteDeadline returns a conservative write deadline based on payload size.
// - targetMBps: assumed sustained throughput (lower = more lenient).
// - We reserve some headroom from overall timeout for server processing/response.
func calcWriteDeadline(timeout time.Duration, sizeBytes int, targetMBps float64) time.Duration {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// Leave headroom for server processing + response
	const reserve = 8 * time.Second
	maxBudget := timeout - reserve
	if maxBudget < 5*time.Second {
		maxBudget = timeout - 1*time.Second
		if maxBudget < 3*time.Second {
			maxBudget = 3 * time.Second
		}
	}

	sizeMB := float64(sizeBytes) / (1024.0 * 1024.0)
	base := 2 * time.Second
	cushion := 5 * time.Second

	// Softer floor: assume ~1 MB/s by default; increase if you like.
	if targetMBps <= 0 {
		targetMBps = 1.0
	}
	est := time.Duration(sizeMB / targetMBps * float64(time.Second))

	writeDL := base + est + cushion

	// Ensure a more generous minimum for big-ish payloads
	if writeDL < 10*time.Second {
		writeDL = 10 * time.Second
	}
	if writeDL > maxBudget {
		writeDL = maxBudget
	}
	return writeDL
}
