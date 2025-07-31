package lumera

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultTimeout    = 30 * time.Second
	defaultLumeraPort = "9090"
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

// newGRPCConnection creates a new gRPC connection. It chooses TLS when the
// address implies HTTPS/grpcs or port 443; otherwise it keeps the previous
// insecure (h2c) behaviour.
func newGRPCConnection(ctx context.Context, rawAddr string) (Connection, error) {
	hostPort, useTLS, serverName, err := normaliseAddr(rawAddr)
	if err != nil {
		return nil, err
	}

	var creds credentials.TransportCredentials
	if useTLS {
		creds = credentials.NewClientTLSFromCert(nil, serverName)
	} else {
		creds = insecure.NewCredentials()
	}

	dialCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	conn, err := grpc.DialContext(
		dialCtx,
		hostPort,
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &grpcConnection{conn: conn}, nil
}

// Accepts all of these:
//
//	https://grpc.testnet.lumera.io           → TLS, host = grpc.testnet.lumera.io:443
//	grpcs://grpc.node9x.com:7443             → TLS, host = grpc.node9x.com:7443
//	grpc.node9x.com:443                      → TLS, host = grpc.node9x.com:443
//	grpc.node9x.com:9090                     → h2c, host = grpc.node9x.com:9090
//	grpc.testnet.lumera.io                   → h2c, host = grpc.testnet.lumera.io:9090
func normaliseAddr(raw string) (hostPort string, useTLS bool, serverName string, err error) {
	// If scheme present, parse as URL first.
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false, "", fmt.Errorf("parse address %q: %w", raw, err)
		}

		host := u.Hostname()
		port := u.Port()
		switch u.Scheme {
		case "https", "grpcs":
			useTLS = true
			if port == "" {
				port = "443"
			}
		case "http", "grpc":
			useTLS = false
			if port == "" {
				port = defaultLumeraPort
			}
		default:
			return "", false, "", fmt.Errorf("unsupported scheme %q in %q", u.Scheme, raw)
		}
		return net.JoinHostPort(host, port), useTLS, host, nil
	}

	// No scheme: split host[:port].
	host, port, splitErr := net.SplitHostPort(raw)
	if splitErr != nil {
		// No port given → assume :9090 / plaintext.
		return net.JoinHostPort(raw, defaultLumeraPort), false, raw, nil
	}

	// Port explicit.
	if port == "443" {
		return net.JoinHostPort(host, port), true, host, nil
	}
	return net.JoinHostPort(host, port), false, host, nil
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
