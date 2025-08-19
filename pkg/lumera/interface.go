//go:generate mockgen -destination=lumera_mock.go -package=lumera -source=interface.go
package lumera

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
)

// Client defines the main interface for interacting with Lumera blockchain
type Client interface {
	Auth() auth.Module
	Action() action.Module
	ActionMsg() action_msg.Module
	SuperNode() supernode.Module
	Tx() tx.Module
	Node() node.Module

	Close() error
}

// NewClient creates a new Lumera client with provided options
func NewClient(ctx context.Context, config *Config) (Client, error) {
	return newClient(ctx, config)
}
