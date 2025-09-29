package lumera

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"os"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

const (
	// Time budget to wait for a candidate to reach READY
	dialReadyTimeout = 10 * time.Second
)

// Default ports to try when none is specified (order matters).
var defaultDialPorts = []string{"9090", "443"}

// Long-lived connection keepalive params
const (
	keepaliveIdleTime       = 10 * time.Minute
	keepaliveAckTimeout     = 20 * time.Second
	reconnectionGracePeriod = 30 * time.Second
)

// Connection defines the interface for a client connection.
type Connection interface {
	Close() error
	GetConn() *grpc.ClientConn
}

// grpcConnection wraps a gRPC connection.
type grpcConnection struct {
	conn *grpc.ClientConn
}

// newGRPCConnection creates a new gRPC connection by racing multiple candidates
// derived from the input address. The first candidate to become READY wins and
// the others are closed. Candidates include TLS and plaintext across common
// ports, depending only on whether a port was explicitly provided.
func newGRPCConnection(ctx context.Context, rawAddr string) (Connection, error) {
	meta, err := parseAddrMeta(rawAddr)
	if err != nil {
		return nil, err
	}

	cands := generateCandidates(meta)
	if len(cands) == 0 {
		return nil, fmt.Errorf("no connection candidates generated for %q", rawAddr)
	}

	// Parent context to cancel all attempts when one succeeds.
	parentCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()

	type result struct {
		conn *grpc.ClientConn
		err  error
		cand dialCandidate
	}

	resCh := make(chan result, len(cands))

	for _, cand := range cands {
		go func(c dialCandidate) {
			creds := insecure.NewCredentials()
			if c.useTLS {
				creds = credentials.NewClientTLSFromCert(nil, c.serverName)
			}

			// Per-attempt timeout
			attemptCtx, cancel := context.WithTimeout(parentCtx, dialReadyTimeout)
			defer cancel()

			conn, err := createGRPCConnection(attemptCtx, c.target, creds)
			resCh <- result{conn: conn, err: err, cand: c}
		}(cand)
	}

	var firstConn *grpc.ClientConn
	var firstCand dialCandidate
	var firstErr error
	var winnerIndex = -1
	// Collect results; return on first success.
	for i := 0; i < len(cands); i++ {
		r := <-resCh
		if r.err == nil && r.conn != nil && winnerIndex == -1 {
			firstConn = r.conn
			firstCand = r.cand
			winnerIndex = i
			// Do not break yet; continue receiving to close any late winners.
			continue
		}
		// Close any non-winning connection to avoid leaks.
		if r.conn != nil {
			_ = r.conn.Close()
		}
		// Keep the first error to report if all fail.
		if firstErr == nil {
			firstErr = r.err
		}
	}

	if firstConn == nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("all connection attempts failed")
		}
		return nil, firstErr
	}

	// Cancel remaining attempts; return the winner.
	cancelAll()

	// Info log showing final selected target and scheme
	scheme := "plaintext"
	if firstCand.useTLS {
		scheme = "tls"
	}
	logtrace.Debug(ctx, "gRPC connection established", logtrace.Fields{
		"target": firstCand.target,
		"scheme": scheme,
	})

	// Start a monitor to terminate the app if connection is lost
	go monitorConnection(ctx, firstConn)

	return &grpcConnection{conn: firstConn}, nil
}

// addressMeta captures parsed input and dialing policy.
type addressMeta struct {
	host            string
	port            string // optional; empty means "unspecified"
	allowBoth       bool
	serverName      string
	hasExplicitPort bool
}

// parseAddrMeta parses the raw address and determines dialing policy.
func parseAddrMeta(raw string) (addressMeta, error) {
	var meta addressMeta

	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return meta, fmt.Errorf("parse address %q: %w", raw, err)
		}
		// Ignore scheme for policy; use only host/port
		meta.host = u.Hostname()
		meta.port = u.Port()
		meta.serverName = meta.host
		meta.hasExplicitPort = meta.port != ""
		meta.allowBoth = true
		return meta, nil
	}

	// No scheme: split host[:port]
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		// No port provided
		meta.host = raw
		meta.port = ""
		meta.serverName = meta.host
		meta.allowBoth = true
		meta.hasExplicitPort = false
		return meta, nil
	}
	meta.host = host
	meta.port = port
	meta.serverName = meta.host
	meta.hasExplicitPort = true
	// With explicit port: try both TLS and plaintext on that port.
	meta.allowBoth = true
	return meta, nil
}

type dialCandidate struct {
	target     string // host:port
	useTLS     bool
	serverName string
}

// generateCandidates builds the set of dial candidates based on addressMeta.
func generateCandidates(meta addressMeta) []dialCandidate {
	// Helper to append unique candidates
	seen := make(map[string]struct{})
	add := func(host, port string, useTLS bool, serverName string, out *[]dialCandidate) {
		if host == "" || port == "" {
			return
		}
		target := net.JoinHostPort(host, port)
		key := target + "|" + fmt.Sprint(useTLS)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		*out = append(*out, dialCandidate{target: target, useTLS: useTLS, serverName: serverName})
	}

	var out []dialCandidate
	// Determine port lists
	var ports []string
	if meta.hasExplicitPort {
		ports = []string{meta.port}
	} else {
		ports = defaultDialPorts
	}

	// Always allow both TLS and plaintext per requirements.
	for _, p := range ports {
		add(meta.host, p, true, meta.serverName, &out)
	}
	for _, p := range ports {
		add(meta.host, p, false, meta.serverName, &out)
	}
	return out
}

// createGRPCConnection creates a gRPC connection with keepalive
func createGRPCConnection(ctx context.Context, hostPort string, creds credentials.TransportCredentials) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                keepaliveIdleTime,
			Timeout:             keepaliveAckTimeout,
			PermitWithoutStream: true,
		}),
	}

	// Establish client connection (non-blocking) then wait until READY.
	conn, err := grpc.NewClient(hostPort, opts...)
	if err != nil {
		return nil, err
	}

	// Start connection attempts and wait for readiness with a bounded timeout.
	conn.Connect()

	// Use provided context deadline if present; otherwise apply a default.
	var cancel context.CancelFunc = func() {}
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, dialReadyTimeout)
	}
	defer cancel()

	for {
		state := conn.GetState()
		switch state {
		case connectivity.Ready:
			return conn, nil
		case connectivity.Shutdown:
			conn.Close()
			return nil, fmt.Errorf("grpc connection is shutdown")
		case connectivity.TransientFailure:
			conn.Close()
			return nil, fmt.Errorf("grpc connection is in transient failure")
		default:
			// Idle or Connecting: wait for a state change or timeout
			if !conn.WaitForStateChange(ctx, state) {
				conn.Close()
				return nil, fmt.Errorf("timeout waiting for grpc connection readiness")
			}
		}
	}
}

// monitorConnection watches the connection state and exits the process if the
// connection transitions to Shutdown or remains in TransientFailure beyond a grace period.
func monitorConnection(ctx context.Context, conn *grpc.ClientConn) {
	for {
		state := conn.GetState()
		switch state {
		case connectivity.Shutdown:
			logtrace.Error(ctx, "gRPC connection shutdown", logtrace.Fields{"action": "exit"})
			os.Exit(1)
		case connectivity.TransientFailure:
			// Allow some time to recover to Ready
			gctx, cancel := context.WithTimeout(ctx, reconnectionGracePeriod)
			for conn.GetState() == connectivity.TransientFailure {
				if !conn.WaitForStateChange(gctx, connectivity.TransientFailure) {
					cancel()
					logtrace.Error(ctx, "gRPC connection lost (transient failure)", logtrace.Fields{"grace": reconnectionGracePeriod.String(), "action": "exit"})
					os.Exit(1)
				}
			}
			cancel()
		default:
			// Idle/Connecting/Ready: just wait for state change
			if !conn.WaitForStateChange(ctx, state) {
				return
			}
		}
	}
}

// Close closes the gRPC connection.
func (c *grpcConnection) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// GetConn returns the underlying gRPC connection.
func (c *grpcConnection) GetConn() *grpc.ClientConn {
	return c.conn
}
