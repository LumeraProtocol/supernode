//go:generate mockgen -destination=dd_mock.go -package=dd -source=interfaces.go

package dd

import "context"

// ClientInterface represents a base connection interface.
type ClientInterface interface {
	// Connect connects to the server at the given address.
	Connect(ctx context.Context, address string) (Connection, error)
}

// Connection represents a client connection
type Connection interface {
	// Close closes connection.
	Close() error

	// DDService returns a new dd-service stream.
	DDService(config *Config) DDService

	// FIXME:
	// Done returns a channel that's closed when connection is shutdown.
	//Done() <-chan struct{}
}

// DDService contains methods for request services from  dd-service.
type DDService interface {
	ImageRarenessScore(ctx context.Context, req RarenessScoreRequest) (ImageRarenessScoreResponse, error)
	GetStatus(ctx context.Context, req GetStatusRequest) (GetStatusResponse, error)
}
