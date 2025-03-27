//go:generate mockgen -destination=rq_mock.go -package=raptorq -source=interfaces.go

package raptorq

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
)

// ClientInterface represents a base connection interface.
type ClientInterface interface {
	// Connect connects to the server at the given address.
	Connect(ctx context.Context, address string) (Connection, error)
}

// Connection represents a client connection
type Connection interface {
	// Close closes connection.
	Close() error

	// RaptorQ returns a new RaptorQ stream.
	RaptorQ(config *Config, lc lumera.Client, store rqstore.Store) RaptorQ

	// FIXME:
	// Done returns a channel that's closed when connection is shutdown.
	//Done() <-chan struct{}
}

// RaptorQ contains methods for request services from RaptorQ service.
type RaptorQ interface {
	// Encode Get map of symbols
	Encode(ctx context.Context, req EncodeRequest) (EncodeResponse, error)
	// Decode returns a path to restored file.
	Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error)
	// EncodeMetaData Get encode info(include encode parameters + symbol id files)
	EncodeMetaData(ctx context.Context, req EncodeMetadataRequest) (EncodeResponse, error)
	// GenRQIdentifiersFiles generates the RQ identifier files
	GenRQIdentifiersFiles(ctx context.Context, req GenRQIdentifiersFilesRequest) (GenRQIdentifiersFilesResponse, error)
}
