package supernode_metrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	lumeravalid "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// checkP2PService performs a lightweight health check against the local P2P
// service using a self-directed DHT ping.
//
// The return value is a normalized "health score" in the range [0, 1]:
//   - 1.0 indicates healthy / reachable
//   - 0.0 indicates unhealthy / unreachable
//
// For now this is stubbed to always return 1.0 but is shaped for richer
// checks (latency, error rates, etc.) in the future.
func (hm *Collector) checkP2PService(ctx context.Context) float64 {
	identity := strings.TrimSpace(hm.identity)
	if identity == "" || hm.keyring == nil || hm.lumeraClient == nil {
		logtrace.Warn(ctx, "P2P health check skipped: missing identity/keyring/client", nil)
		return 0.0
	}

	host := hm.getPublicIP(ctx)
	if host == "" {
		host = "127.0.0.1"
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", P2PPort))
	if hm.p2pPort != 0 {
		target = net.JoinHostPort(host, fmt.Sprintf("%d", hm.p2pPort))
	}

	// Build client credentials using the same ALTS/securekeyx stack as external
	// P2P clients. We treat the local node as a simplenode peer dialing the
	// supernode (itself) by identity.
	clientCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       hm.keyring,
			LocalIdentity: identity,
			PeerType:      securekeyx.Simplenode,
			Validator:     lumeravalid.NewSecureKeyExchangeValidator(hm.lumeraClient),
		},
	})
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("P2P health check: failed to create client credentials: %v", err), nil)
		return 0.0
	}

	lumeraTC, ok := clientCreds.(*ltc.LumeraTC)
	if !ok {
		logtrace.Error(ctx, "P2P health check: invalid credentials type (expected *LumeraTC)", nil)
		return 0.0
	}
	// Remote identity is the supernode itself.
	lumeraTC.SetRemoteIdentity(identity)

	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(PortCheckTimeoutSeconds)*time.Second)
	defer cancel()

	var d net.Dialer
	rawConn, err := d.DialContext(checkCtx, "tcp", target)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("P2P health check: failed to dial %s: %v", target, err), nil)
		return 0.0
	}
	defer rawConn.Close()

	// Ensure the handshake cannot hang indefinitely.
	_ = rawConn.SetDeadline(time.Now().UTC().Add(time.Duration(PortCheckTimeoutSeconds) * time.Second))

	secureConn, _, err := lumeraTC.ClientHandshake(checkCtx, "", rawConn)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("P2P health check: handshake failed against %s: %v", target, err), nil)
		return 0.0
	}
	_ = secureConn.Close()

	// If we reach this point the node accepted a full secure handshake on the
	// P2P port, which is sufficient to treat the service as healthy.
	return 1.0
}

// checkStatusAPI performs an HTTP GET to the external /api/v1/status endpoint
// exposed by the gateway to validate that the REST API is reachable and
// functioning.
//
// Like other health checks, the result is a [0, 1] score. This function
// currently serves as a placeholder; once wired to a concrete HTTP client
// it will surface connectivity and status-code level failures.
func (hm *Collector) checkStatusAPI(ctx context.Context) float64 {
	host := hm.getPublicIP(ctx)
	if host == "" {
		host = "127.0.0.1"
	}

	port := StatusPort
	if hm.gatewayPort != 0 {
		port = int(hm.gatewayPort)
	}
	url := fmt.Sprintf("http://%s:%d/api/v1/status", host, port)

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(PortCheckTimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("Status API health check: failed to build request: %v", err), nil)
		return 0.0
	}

	client := &http.Client{
		Timeout: time.Duration(PortCheckTimeoutSeconds) * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("Status API health check: request failed to %s: %v", url, err), nil)
		return 0.0
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 1.0
	}

	logtrace.Error(ctx, fmt.Sprintf("Status API health check: non-success status %d from %s", resp.StatusCode, url), nil)
	return 0.0
}

// checkGRPCService performs a gRPC self-health check against the public
// supernode gRPC endpoint using the same ALTS/TLS configuration that external
// clients use.
//
// The metric is expressed as a [0, 1] score to make it easy to aggregate or
// combine with additional health signals in the future.
func (hm *Collector) checkGRPCService(ctx context.Context) float64 {
	identity := strings.TrimSpace(hm.identity)
	if identity == "" || hm.keyring == nil || hm.lumeraClient == nil {
		logtrace.Warn(ctx, "gRPC health check skipped: missing identity/keyring/client", nil)
		return 0.0
	}

	host := hm.getPublicIP(ctx)
	if host == "" {
		host = "127.0.0.1"
	}
	port := APIPort
	if hm.grpcPort != 0 {
		port = int(hm.grpcPort)
	}
	grpcEndpoint := fmt.Sprintf("%s:%d", host, port)

	// Build client credentials mirroring external secure supernode clients.
	clientCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       hm.keyring,
			LocalIdentity: identity,
			PeerType:      securekeyx.Simplenode,
			Validator:     lumeravalid.NewSecureKeyExchangeValidator(hm.lumeraClient),
		},
	})
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("gRPC health check: failed to create client credentials: %v", err), nil)
		return 0.0
	}

	// Format address as "identity@host:port" so the ALTS layer knows which
	// supernode identity we expect on the remote end (ourselves).
	target := ltc.FormatAddressWithIdentity(identity, grpcEndpoint)

	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(PortCheckTimeoutSeconds)*time.Second)
	defer cancel()

	grpcClient := grpcclient.NewClient(clientCreds)
	conn, err := grpcClient.Connect(checkCtx, target, grpcclient.DefaultClientOptions())
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("gRPC health check: connection failed to %s: %v", grpcEndpoint, err), nil)
		return 0.0
	}
	defer conn.Close()

	healthClient := grpc_health_v1.NewHealthClient(conn)
	resp, err := healthClient.Check(checkCtx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("gRPC health check: health RPC failed for %s: %v", grpcEndpoint, err), nil)
		return 0.0
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		logtrace.Error(ctx, fmt.Sprintf("gRPC health check: service not serving (status=%v) at %s", resp.Status, grpcEndpoint), nil)
		return 0.0
	}

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
