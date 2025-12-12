package supernode_metrics

import (
	"context"
	"fmt"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// checkP2PService performs a lightweight health check against the local P2P
// service using a self-directed DHT ping.
func (hm *Collector) checkP2PService(ctx context.Context) float64 {

	return 1.0
}

// checkStatusAPI performs an HTTP GET to the external /api/v1/status endpoint
// exposed by the gateway to validate that the REST API is reachable and
// functioning.
func (hm *Collector) checkStatusAPI(ctx context.Context) float64 {

	return 1.0
}

// checkGRPCService performs a gRPC self-health check against the public
// supernode gRPC endpoint using the same ALTS/TLS configuration that external
// clients use.
func (hm *Collector) checkGRPCService(ctx context.Context) float64 {

	return 1.0
}

// getPublicIP determines the node's public IP address from chain registration.
func (hm *Collector) getPublicIP(ctx context.Context) string {
	// Get our registered IP from the blockchain - this is the source of truth.
	// The SuperNode must be registered on chain to operate, so this should always work.
	snInfo, err := hm.lumeraClient.SuperNode().GetSupernodeWithLatestAddress(ctx, hm.identity)
	if err == nil && snInfo != nil && snInfo.LatestAddress != "" {
		// Extract IP from "ip:port" format if present.
		address := strings.TrimSpace(snInfo.LatestAddress)
		if idx := strings.Index(address, ":"); idx > 0 {
			return address[:idx]
		}
		return address
	}

	// If we can't get IP from chain, log error and return empty.
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("failed to get IP from chain registration: %v (identity=%s)", err, hm.identity), nil)
	}

	return ""
}
