package kademlia

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

const (
	// Ping the target to check if it's online
	Ping = iota
	// StoreData iterative store data in kademlia network
	StoreData
	// FindNode iterative find node in kademlia network
	FindNode
	// FindValue iterative find value in kademlia network
	FindValue
	// Replicate is replicate data request towards a network node
	Replicate
	// BatchFindValues finds values in kademlia network
	BatchFindValues
	// StoreBatchData iterative stores data in batches
	BatchStoreData
	// BatchFindNode iterative find node in kademlia network
	BatchFindNode
	// BatchGetValues finds values in kademlia network
	BatchGetValues
)

func init() {
	gob.Register(&ResponseStatus{})
	gob.Register(&FindNodeRequest{})
	gob.Register(&FindNodeResponse{})
	gob.Register(&FindValueRequest{})
	gob.Register(&FindValueResponse{})
	gob.Register(&StoreDataRequest{})
	gob.Register(&StoreDataResponse{})
	gob.Register(&ReplicateDataRequest{})
	gob.Register(&ReplicateDataResponse{})
	gob.Register(&BatchFindValuesRequest{})
	gob.Register(&BatchFindValuesResponse{})
	gob.Register(&BatchStoreDataRequest{})
	gob.Register(&BatchFindNodeRequest{})
	gob.Register(&BatchFindNodeResponse{})
	gob.Register(&BatchGetValuesRequest{})
	gob.Register(&BatchGetValuesResponse{})
}

type MessageWithError struct {
	Message *Message
	Error   error
	// Extended context for store RPCs
	KeysCount  int   // number of items attempted in this RPC
	Receiver   *Node // receiver node info (target)
	DurationMS int64 // duration of the RPC in milliseconds
}

// Message structure for kademlia network
type Message struct {
	Sender      *Node       // the sender node
	Receiver    *Node       // the receiver node
	MessageType int         // the message type
	Data        interface{} // the real data for the request
	// CorrelationID carries a best-effort trace identifier so that logs
	// across nodes can be joined in external systems.
	CorrelationID string
	// Origin carries the phase that produced this message (first_pass | worker | download)
	Origin string
}

func (m *Message) String() string {
	return fmt.Sprintf("type: %v, sender: %v, receiver: %v, data type: %T", m.MessageType, m.Sender.String(), m.Receiver.String(), m.Data)
}

// ResultType specify success of message request
type ResultType int

const (
	// ResultOk means request is ok
	ResultOk ResultType = 0
	// ResultFailed meas request got failed
	ResultFailed ResultType = 1
)

// ReplicateDataRequest ...
type ReplicateDataRequest struct {
	Keys []string
}

// ReplicateDataResponse ...
type ReplicateDataResponse struct {
	Status ResponseStatus
}

// ResponseStatus defines the result of request
type ResponseStatus struct {
	Result ResultType
	ErrMsg string
}

// FindNodeRequest defines the request data for find node
type FindNodeRequest struct {
	Target []byte
}

// FindNodeResponse defines the response data for find node
type FindNodeResponse struct {
	Status  ResponseStatus
	Closest []*Node
}

// FindValueRequest defines the request data for find value
type FindValueRequest struct {
	Target []byte
}

// FindValueResponse defines the response data for find value
type FindValueResponse struct {
	Status  ResponseStatus
	Closest []*Node
	Value   []byte
}

// StoreDataRequest defines the request data for store data
type StoreDataRequest struct {
	Data []byte
	Type int
}

// StoreDataResponse defines the response data for store data
type StoreDataResponse struct {
	Status ResponseStatus
}

// BatchFindValuesRequest defines the request data for find value
type BatchFindValuesRequest struct {
	Keys []string
}

// BatchFindValuesResponse defines the response data for find value
type BatchFindValuesResponse struct {
	Status   ResponseStatus
	Response []byte
	Done     bool
}

type KeyValWithClosest struct {
	Value   []byte
	Closest []*Node
}

// BatchGetValuesRequest defines the request data for find value
type BatchGetValuesRequest struct {
	Data map[string]KeyValWithClosest //(compressed) // keys are hex encoded
	//Data []byte
}

type BatchGetValuesResponse struct {
	Data map[string]KeyValWithClosest // keys are hex encoded
	//Data   []byte
	Status ResponseStatus
}

// encode the message
// encodePayload gob-encodes the message and returns only the payload bytes (no header).
// Callers can write the 8-byte header and payload separately to avoid duplicating large buffers.
func encodePayload(message *Message) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(message); err != nil {
		return nil, err
	}
	// Check against absolute maximum first
	const maxMessageSize = 500 * 1024 * 1024 // 500MB absolute max
	if buf.Len() > maxMessageSize {
		return nil, errors.New("message size exceeds absolute maximum")
	}
	if utils.BytesIntToMB(buf.Len()) > defaultMaxPayloadSize {
		return nil, errors.New("payload too big")
	}
	return buf.Bytes(), nil
}

// encode builds the full on-wire message (header + payload) as a single slice.
// Legacy callers may use this; new code should prefer encodePayload and write header+payload separately.
func encode(message *Message) ([]byte, error) {
	payload, err := encodePayload(message)
	if err != nil {
		return nil, err
	}
	var header [8]byte
	binary.PutUvarint(header[:], uint64(len(payload)))
	out := make([]byte, 0, len(header)+len(payload))
	out = append(out, header[:]...)
	out = append(out, payload...)
	return out, nil
}

// decode the message
func decode(conn io.Reader) (*Message, error) {
	// read the header (fixed 8 bytes carrying a uvarint length)
	header := make([]byte, 8)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}

	// parse the length of message
	length, err := binary.ReadUvarint(bytes.NewBuffer(header))
	if err != nil {
		return nil, errors.Errorf("parse header length: %w", err)
	}

	// Check against absolute maximum first to prevent DoS
	const maxMessageSize = 500 * 1024 * 1024 // 500MB absolute max
	if length > maxMessageSize {
		return nil, errors.New("message size exceeds absolute maximum")
	}
	if utils.BytesToMB(length) > defaultMaxPayloadSize {
		return nil, errors.New("payload too big")
	}

	// Stream-decode directly from the connection without allocating a full buffer
	lr := &io.LimitedReader{R: conn, N: int64(length)}
	dec := gob.NewDecoder(lr)
	msg := &Message{}
	if err := dec.Decode(msg); err != nil {
		return nil, err
	}
	// If gob didn't consume exactly 'length' bytes, drain the remainder to keep the stream aligned
	if lr.N > 0 {
		// best-effort drain; ignore errors to avoid perturbing the handler
		_, _ = io.CopyN(io.Discard, lr, lr.N)
	}
	return msg, nil
}

// BatchStoreDataRequest defines the request data for store data
type BatchStoreDataRequest struct {
	Data [][]byte
	Type int
}

// BatchFindNodeRequest defines the request data for find node
type BatchFindNodeRequest struct {
	HashedTarget [][]byte
}

// BatchFindNodeResponse defines the response data for find node
type BatchFindNodeResponse struct {
	Status       ResponseStatus
	ClosestNodes map[string][]*Node
}
