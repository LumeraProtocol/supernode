package audit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/credentials"

	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

type Prober struct {
	grpcCreds credentials.TransportCredentials
	grpcOpts  *grpcclient.ClientOptions
	p2pCreds  *ltc.LumeraTC
	httpClient *http.Client
}

func NewProber(lumeraClient lumera.Client, keyring cKeyring.Keyring, selfIdentity string, timeout time.Duration) (*Prober, error) {
	selfIdentity = strings.TrimSpace(selfIdentity)
	if lumeraClient == nil {
		return nil, fmt.Errorf("lumera client is nil")
	}
	if keyring == nil {
		return nil, fmt.Errorf("keyring is nil")
	}
	if selfIdentity == "" {
		return nil, fmt.Errorf("self identity is empty")
	}
	if timeout <= 0 {
		timeout = ProbeTimeout
	}

	validator := lumera.NewSecureKeyExchangeValidator(lumeraClient)

	grpcCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       keyring,
			LocalIdentity: selfIdentity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create gRPC client creds: %w", err)
	}

	grpcProbeOpts := grpcclient.DefaultClientOptions()
	grpcProbeOpts.EnableRetries = false
	grpcProbeOpts.ConnWaitTime = timeout
	grpcProbeOpts.MinConnectTimeout = timeout

	p2pCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       keyring,
			LocalIdentity: selfIdentity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create P2P client creds: %w", err)
	}

	lumeraTC, ok := p2pCreds.(*ltc.LumeraTC)
	if !ok {
		return nil, fmt.Errorf("invalid P2P creds type (expected *LumeraTC)")
	}

	return &Prober{
		grpcCreds:  grpcCreds,
		grpcOpts:   grpcProbeOpts,
		p2pCreds:   lumeraTC,
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

func (p *Prober) ProbePortState(ctx context.Context, targetIdentity, hostIPv4 string, port uint32, timeout time.Duration) audittypes.PortState {
	targetIdentity = strings.TrimSpace(targetIdentity)
	hostIPv4 = strings.TrimSpace(hostIPv4)
	if targetIdentity == "" || hostIPv4 == "" || port == 0 {
		return audittypes.PortState_PORT_STATE_UNKNOWN
	}
	if timeout <= 0 {
		timeout = ProbeTimeout
	}

	switch port {
	case APIPort:
		grpcCreds := p.grpcCreds
		if grpcCreds == nil {
			return audittypes.PortState_PORT_STATE_UNKNOWN
		}
		grpcClient := grpcclient.NewClient(grpcCreds.Clone())
		if err := probeGRPC(ctx, grpcClient, p.grpcOpts, targetIdentity, hostIPv4, int(port), timeout); err != nil {
			if isConnRefused(err) {
				return audittypes.PortState_PORT_STATE_CLOSED
			}
			return audittypes.PortState_PORT_STATE_UNKNOWN
		}
		return audittypes.PortState_PORT_STATE_OPEN
	case P2PPort:
		if p.p2pCreds == nil {
			return audittypes.PortState_PORT_STATE_UNKNOWN
		}
		credsAny := p.p2pCreds.Clone()
		creds, ok := credsAny.(*ltc.LumeraTC)
		if !ok || creds == nil {
			return audittypes.PortState_PORT_STATE_UNKNOWN
		}
		if err := probeP2P(ctx, creds, targetIdentity, hostIPv4, int(port), timeout); err != nil {
			if isConnRefused(err) {
				return audittypes.PortState_PORT_STATE_CLOSED
			}
			return audittypes.PortState_PORT_STATE_UNKNOWN
		}
		return audittypes.PortState_PORT_STATE_OPEN
	case StatusPort:
		if err := probeGateway(ctx, p.httpClient, hostIPv4, int(port), timeout); err != nil {
			if isConnRefused(err) {
				return audittypes.PortState_PORT_STATE_CLOSED
			}
			return audittypes.PortState_PORT_STATE_UNKNOWN
		}
		return audittypes.PortState_PORT_STATE_OPEN
	default:
		// Unknown port type: do a minimal TCP dial so we still return a meaningful
		// port state if the chain config expands required_open_ports.
		return probeTCPPort(ctx, hostIPv4, port, timeout)
	}
}

func probeGRPC(ctx context.Context, client *grpcclient.Client, opts *grpcclient.ClientOptions, identity, host string, port int, timeout time.Duration) error {
	if client == nil {
		return fmt.Errorf("gRPC client is nil")
	}

	hostPort := net.JoinHostPort(host, strconv.Itoa(port))
	addr := ltc.FormatAddressWithIdentity(identity, hostPort)

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := client.Connect(probeCtx, addr, opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	healthClient := grpc_health_v1.NewHealthClient(conn)
	resp, err := healthClient.Check(probeCtx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("health status=%v", resp.GetStatus())
	}
	return nil
}

func probeP2P(ctx context.Context, creds *ltc.LumeraTC, identity, host string, port int, timeout time.Duration) error {
	if creds == nil {
		return fmt.Errorf("p2p creds is nil")
	}
	creds.SetRemoteIdentity(identity)

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	rawConn, err := d.DialContext(probeCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer rawConn.Close()

	_ = rawConn.SetDeadline(time.Now().Add(timeout))

	secureConn, _, err := creds.ClientHandshake(probeCtx, "", rawConn)
	if err != nil {
		return err
	}
	_ = secureConn.Close()
	return nil
}

func probeGateway(ctx context.Context, client *http.Client, host string, port int, timeout time.Duration) error {
	if client == nil {
		return fmt.Errorf("http client is nil")
	}

	urlStr := gatewayStatusURL(host, port)
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	return nil
}

func gatewayStatusURL(host string, port int) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	hostPort := net.JoinHostPort(host, strconv.Itoa(port))
	u := url.URL{Scheme: "http", Host: hostPort, Path: "/api/v1/status"}
	return u.String()
}

func probeTCPPort(ctx context.Context, host string, port uint32, timeout time.Duration) audittypes.PortState {
	if host == "" || port == 0 {
		return audittypes.PortState_PORT_STATE_UNKNOWN
	}
	if timeout <= 0 {
		timeout = ProbeTimeout
	}

	dialer := &net.Dialer{Timeout: timeout}
	addr := fmt.Sprintf("%s:%d", host, port)

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err == nil {
		_ = conn.Close()
		return audittypes.PortState_PORT_STATE_OPEN
	}

	if isConnRefused(err) {
		return audittypes.PortState_PORT_STATE_CLOSED
	}

	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return audittypes.PortState_PORT_STATE_UNKNOWN
	}

	return audittypes.PortState_PORT_STATE_UNKNOWN
}

func isConnRefused(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return errors.Is(err, syscall.ECONNREFUSED)
	}

	var syscallErr *os.SyscallError
	if errors.As(opErr.Err, &syscallErr) {
		return errors.Is(syscallErr.Err, syscall.ECONNREFUSED)
	}

	return errors.Is(opErr.Err, syscall.ECONNREFUSED)
}
