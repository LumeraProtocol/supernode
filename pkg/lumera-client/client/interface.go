// Package client defines the main interface for interacting with Lumera blockchain
package client

import (
	"context"

	"lumera-client/modules/action"
	"lumera-client/modules/node"
	"lumera-client/modules/supernode"
	"lumera-client/modules/tx"
)

// Client defines the main interface for interacting with Lumera blockchain
type Client interface {
	// Module accessors
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
