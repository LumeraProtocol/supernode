package kademlia

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/btcsuite/btcutil/base58"
	json "github.com/json-iterator/go"

	"github.com/LumeraProtocol/supernode/pkg/utils"

	"github.com/google/uuid"
	"go.uber.org/ratelimit"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/credentials"
)

const (
	defaultConnDeadline                = 10 * time.Minute
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
		BatchStoreData: 60 * time.Second,
		FindNode:       30 * time.Second,
		BatchFindNode:  15 * time.Second,
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
	tc          credentials.TransportCredentials
	connPool    *ConnPool
	connPoolMtx sync.Mutex
	sem         *semaphore.Weighted
}

// NewNetwork returns a network service
func NewNetwork(ctx context.Context, dht *DHT, self *Node, tc credentials.TransportCredentials) (*Network, error) {
	s := &Network{
		dht:      dht,
		self:     self,
		done:     make(chan struct{}),
		tc:       tc,
		connPool: NewConnPool(ctx),
		sem:      semaphore.NewWeighted(maxConcurrentFindBatchValsRequests),
	}
	// init the rate limiter
	s.limiter = ratelimit.New(defaultConnRate)

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
	if s.tc != nil {
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
	// do secure handshaking
	if s.tc != nil {
		conn, err = NewSecureServerConn(ctx, s.tc, rawConn)
		if err != nil {
			rawConn.Close()
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

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// read the request from connection
		request, err := decode(conn)
		if err != nil {
			if err == io.EOF {
				return
			}
			logtrace.Warn(ctx, "Read and decode failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
			return
		}
		reqID := uuid.New().String()

		var response []byte
		switch request.MessageType {
		case FindNode:
			encoded, err := s.handleFindNode(ctx, request)
			if err != nil {
				logtrace.Error(ctx, "Handle find node request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})

				return
			}
			response = encoded
		case BatchFindNode:
			encoded, err := s.handleBatchFindNode(ctx, request)
			if err != nil {
				logtrace.Error(ctx, "Handle batch find node request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				return
			}
			response = encoded
		case FindValue:
			// handle the request for finding value
			encoded, err := s.handleFindValue(ctx, request)
			if err != nil {
				logtrace.Error(ctx, "Handle find value request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				return
			}
			response = encoded
		case Ping:
			encoded, err := s.handlePing(ctx, request)
			if err != nil {
				logtrace.Error(ctx, "Handle ping request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				return
			}
			response = encoded
		case StoreData:
			// handle the request for storing data
			encoded, err := s.handleStoreData(ctx, request)
			if err != nil {
				logtrace.Error(ctx, "Handle store data request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				return
			}
			response = encoded
		case Replicate:
			// handle the request for replicate request
			encoded, err := s.handleReplicate(ctx, request)
			if err != nil {
				logtrace.Error(ctx, "Handle replicate request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				return
			}
			response = encoded
		case BatchFindValues:
			// handle the request for finding value
			encoded, err := s.handleBatchFindValues(ctx, request, reqID)
			if err != nil {
				logtrace.Error(ctx, "Handle batch find values request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
					"p2p-req-id":         reqID,
				})
				return
			}
			response = encoded
		case BatchStoreData:
			// handle the request for storing data
			encoded, err := s.handleBatchStoreData(ctx, request)
			if err != nil {
				logtrace.Error(ctx, "Handle batch store data request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				return
			}
			response = encoded
		case BatchGetValues:
			// handle the request for finding value
			encoded, err := s.handleGetValuesRequest(ctx, request, reqID)
			if err != nil {
				logtrace.Error(ctx, "Handle batch get values request failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
					"p2p-req-id":         reqID,
				})
				return
			}
			response = encoded
		default:
			logtrace.Error(ctx, "Invalid message type", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"message-type":       request.MessageType,
			})
			return
		}

		// write the response
		if _, err := conn.Write(response); err != nil {
			logtrace.Error(ctx, "Write failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
				"p2p-req-id":         reqID,
				"message-type":       request.MessageType,
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
				logtrace.Error(ctx, "Socket accept failed, retrying", logtrace.Fields{
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
		logtrace.Error(ctx, "Invalid receiver port", logtrace.Fields{
			logtrace.FieldModule: "p2p",
		})
		return nil, errors.New("invalid receiver port")
	}
	if request.Sender != nil && request.Sender.Port == 50052 {
		logtrace.Error(ctx, "Invalid sender port", logtrace.Fields{
			logtrace.FieldModule: "p2p",
		})
		return nil, errors.New("invalid sender port")
	}

	remoteAddr := fmt.Sprintf("%s@%s:%d", string(request.Receiver.ID), request.Receiver.IP, request.Receiver.Port)

	if s.tc == nil {
		return nil, errors.New("secure transport credentials are not set")
	}

	// do secure handshaking
	s.connPoolMtx.Lock()
	conn, err := s.connPool.Get(remoteAddr)
	if err != nil {
		conn, err = NewSecureClientConn(ctx, s.tc, remoteAddr)
		if err != nil {
			s.connPoolMtx.Unlock()
			return nil, errors.Errorf("client secure establish %q: %w", remoteAddr, err)
		}
		s.connPool.Add(remoteAddr, conn)
	}
	s.connPoolMtx.Unlock()

	defer func() {
		if err != nil && s.tc != nil {
			s.connPoolMtx.Lock()
			defer s.connPoolMtx.Unlock()

			conn.Close()
			s.connPool.Del(remoteAddr)
		}
	}()

	// encode and send the request message
	data, err := encode(request)
	if err != nil {
		return nil, errors.Errorf("encode: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return nil, errors.Errorf("conn write: %w", err)
	}

	// receive and decode the response message
	response, err := decode(conn)
	if err != nil {
		return nil, errors.Errorf("conn read: %w", err)
	}

	return response, nil
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
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, BatchGetValues); response != nil || err != nil {
			res = response
		}
	}()

	request, ok := message.Data.(*BatchGetValuesRequest)
	if !ok {
		err := errors.New("invalid BatchGetValuesRequest")
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
	defer func() {
		if response, err := s.handlePanic(ctx, message.Sender, BatchStoreData); response != nil || err != nil {
			res = response
		}
	}()

	request, ok := message.Data.(*BatchStoreDataRequest)
	if !ok {
		err := errors.New("invalid BatchStoreDataRequest")
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
	case FindNode, BatchFindNode:
		response = &BatchFindNodeResponse{Status: responseStatus}
	case FindValue, BatchFindValues:
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
