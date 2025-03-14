package supernode

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/raptorq"
)

// ClientInterface represents a base connection interface.
type ClientInterface interface {
	// Connect connects to the server at the given address.
	Connect(ctx context.Context, address string) (ConnectionInterface, error)
}

// ConnectionInterface represents a client connection
type ConnectionInterface interface {
	// Close closes connection.
	Close() error
	// RegisterCascade returns a new RegisterCascade stream
	RegisterCascade() RegisterCascadeInterface
}

// SuperNodePeerAPIInterface base interface for other Node API interfaces
type SuperNodePeerAPIInterface interface {
	// SessID returns the taskID received from the server during the handshake.
	SessID() (taskID string)
	// Session sets up an initial connection with primary supernode, by telling sessID and its own nodeID.
	Session(ctx context.Context, nodeID, sessID string) (err error)
}

// revive:disable:exported

// NodeMaker interface to make concrete node types
type NodeMaker interface {
	MakeNode(conn ConnectionInterface) SuperNodePeerAPIInterface
}

// RegisterCascadeInterface represents an interaction stream with supernodes for registering sense.
type RegisterCascadeInterface interface {
	SuperNodePeerAPIInterface

	// Send signature of ticket to primary supernode
	SendCascadeTicketSignature(ctx context.Context, nodeID string, signature []byte, data []byte, rqFile []byte, rqEncodeParams raptorq.EncoderParameters) error
}
