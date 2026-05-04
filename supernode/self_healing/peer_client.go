package self_healing

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/v2/pkg/netutil"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// secureVerifierFetcher implements VerifierFetcher by dialing the assigned
// healer over the same secure-rpc / lumeraid stack the legacy
// storage_challenge loop uses.
type secureVerifierFetcher struct {
	lumera      lumera.Client
	kr          keyring.Keyring
	self        string
	defaultPort uint16

	mu         sync.Mutex
	grpcClient *grpcclient.Client
	grpcOpts   *grpcclient.ClientOptions
}

// NewSecureVerifierFetcher constructs the production-grade VerifierFetcher
// for the LEP-6 §19 healer-served path. self is the local supernode
// identity; defaultPort is the supernode gRPC port to fall back to when the
// chain-registered address omits a port.
func NewSecureVerifierFetcher(client lumera.Client, kr keyring.Keyring, self string, defaultPort uint16) VerifierFetcher {
	return &secureVerifierFetcher{
		lumera:      client,
		kr:          kr,
		self:        strings.TrimSpace(self),
		defaultPort: defaultPort,
	}
}

func (f *secureVerifierFetcher) ensureClient() error {
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
	f.grpcOpts.EnableRetries = false // verifier orchestrates retries itself
	return nil
}

// FetchReconstructed dials healerAccount and streams the reconstructed
// bytes for healOpID, returning the concatenated payload.
func (f *secureVerifierFetcher) FetchReconstructed(ctx context.Context, healOpID uint64, healerAccount, verifierAccount string) ([]byte, error) {
	if err := f.ensureClient(); err != nil {
		return nil, err
	}
	info, err := f.lumera.SuperNode().GetSupernodeWithLatestAddress(ctx, healerAccount)
	if err != nil || info == nil {
		return nil, fmt.Errorf("resolve healer %q: %w", healerAccount, err)
	}
	raw := strings.TrimSpace(info.LatestAddress)
	if raw == "" {
		return nil, fmt.Errorf("no address for healer %q", healerAccount)
	}
	host, port, ok := netutil.ParseHostAndPort(raw, int(f.defaultPort))
	if !ok || strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("invalid healer address %q", raw)
	}
	addr := net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
	conn, err := f.grpcClient.Connect(ctx, fmt.Sprintf("%s@%s", strings.TrimSpace(healerAccount), addr), f.grpcOpts)
	if err != nil {
		return nil, fmt.Errorf("dial healer %q: %w", healerAccount, err)
	}
	defer conn.Close()
	client := supernode.NewSelfHealingServiceClient(conn)
	stream, err := client.ServeReconstructedArtefacts(ctx, &supernode.ServeReconstructedArtefactsRequest{
		HealOpId:        healOpID,
		VerifierAccount: verifierAccount,
	})
	if err != nil {
		return nil, fmt.Errorf("open serve stream: %w", err)
	}
	var buf []byte
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, fmt.Errorf("recv: %w", err)
		}
		buf = append(buf, msg.Chunk...)
		if msg.IsLast {
			// Drain any trailer.
			_, _ = stream.Recv()
			return buf, nil
		}
	}
}
