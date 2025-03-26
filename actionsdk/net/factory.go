package net

import (
	"context"
	"fmt"
	"strings"

	"action/adapters/lumera"
	"action/config"
)

// ClientFactory creates supernode clients
type ClientFactory struct {
	config config.Config
}

// NewClientFactory creates a new supernode client factory
func NewClientFactory(config config.Config) *ClientFactory {
	return &ClientFactory{
		config: config,
	}
}

// CreateClient creates a client for a specific supernode
func (f *ClientFactory) CreateClient(ctx context.Context, supernode lumera.Supernode) (SupernodeClient, error) {
	// Validate the supernode has an endpoint
	if supernode.GrpcEndpoint == "" {
		return nil, fmt.Errorf("supernode has no gRPC endpoint")
	}

	// Ensure endpoint has port
	endpoint := EnsureEndpointHasPort(supernode.GrpcEndpoint, f.config.DefaultSupernodePort)

	// Update the supernode with the properly formatted endpoint
	supernode.GrpcEndpoint = endpoint

	// Create client config
	clientConfig := NewConfigFromGlobalConfig(f.config, supernode)

	// Create client
	return NewSupernodeClient(ctx, clientConfig)
}

// EnsureEndpointHasPort adds default port to endpoint if missing
func EnsureEndpointHasPort(endpoint string, defaultPort int) string {
	if !strings.Contains(endpoint, ":") {
		return fmt.Sprintf("%s:%d", endpoint, defaultPort)
	}
	return endpoint
}
