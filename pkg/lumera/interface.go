//go:generate mockgen -destination=lumera_mock.go -package=lumera -source=interface.go
package lumera

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"
)

// Client defines the main interface for interacting with Lumera blockchain
type Client interface {
	Action() action.Module
	SuperNode() supernode.Module
	Tx() tx.Module
	Node() node.Module

	Close() error
}

// NewClient creates a new Lumera client with provided options
func NewClient(ctx context.Context, opts ...Option) (Client, error) {
	return newClient(ctx, opts...)
}
