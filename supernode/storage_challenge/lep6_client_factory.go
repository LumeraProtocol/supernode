package storage_challenge

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
)

// secureSupernodeClientFactory dials peer supernodes using the same secure
// gRPC stack the legacy storage_challenge loop uses (see
// service.go::callGetSliceProof). It is the production implementation of
// SupernodeClientFactory wired by supernode/cmd/start.go.
type secureSupernodeClientFactory struct {
	lumera      lumera.Client
	kr          keyring.Keyring
	self        string
	defaultPort uint16

	mu         sync.Mutex
	grpcClient *grpcclient.Client
	grpcOpts   *grpcclient.ClientOptions
}

// NewSecureSupernodeClientFactory builds a SupernodeClientFactory backed by
// the secure key-exchange gRPC stack. self is the local identity used in the
// ALTS handshake; defaultPort is the supernode port to fall back to when the
// chain-registered LatestAddress contains only a host.
func NewSecureSupernodeClientFactory(client lumera.Client, kr keyring.Keyring, self string, defaultPort uint16) SupernodeClientFactory {
	return &secureSupernodeClientFactory{
		lumera:      client,
		kr:          kr,
		self:        strings.TrimSpace(self),
		defaultPort: defaultPort,
	}
}

func (f *secureSupernodeClientFactory) ensureClient() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.grpcClient != nil {
		return nil
	}
	validator := lumera.NewSecureKeyExchangeValidator(f.lumera)
	creds, err := credentials.NewClientCreds(&credentials.ClientOptions{
		CommonOptions: credentials.CommonOptions{
			Keyring:       f.kr,
			LocalIdentity: f.self,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	if err != nil {
		return fmt.Errorf("create secure gRPC client creds: %w", err)
	}
	f.grpcClient = grpcclient.NewClient(creds)
	f.grpcOpts = grpcclient.DefaultClientOptions()
	f.grpcOpts.EnableRetries = true
	return nil
}

// Dial resolves the peer's chain-registered address and opens a secure
// gRPC connection. The returned SupernodeCompoundClient holds onto the
// underlying *grpc.ClientConn and closes it on Close().
func (f *secureSupernodeClientFactory) Dial(ctx context.Context, target string) (SupernodeCompoundClient, error) {
	if err := f.ensureClient(); err != nil {
		return nil, err
	}
	info, err := f.lumera.SuperNode().GetSupernodeWithLatestAddress(ctx, target)
	if err != nil || info == nil {
		return nil, fmt.Errorf("resolve target %q: %w", target, err)
	}
	raw := strings.TrimSpace(info.LatestAddress)
	if raw == "" {
		return nil, fmt.Errorf("no address for target %q", target)
	}
	host, port, ok := parseHostAndPort(raw, int(f.defaultPort))
	if !ok || strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("invalid address %q for target %q", raw, target)
	}
	addr := net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
	conn, err := f.grpcClient.Connect(ctx, fmt.Sprintf("%s@%s", strings.TrimSpace(target), addr), f.grpcOpts)
	if err != nil {
		return nil, fmt.Errorf("dial target %q: %w", target, err)
	}
	return &secureCompoundClient{conn: conn, client: supernode.NewStorageChallengeServiceClient(conn)}, nil
}

type secureCompoundClient struct {
	conn   *grpc.ClientConn
	client supernode.StorageChallengeServiceClient
}

func (c *secureCompoundClient) GetCompoundProof(ctx context.Context, req *supernode.GetCompoundProofRequest) (*supernode.GetCompoundProofResponse, error) {
	return c.client.GetCompoundProof(ctx, req)
}

func (c *secureCompoundClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
