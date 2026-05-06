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
	// H7 fix: bound the verifier-side accumulator so a buggy or
	// malicious healer cannot OOM the verifier by streaming more than
	// MaxReconstructedBytes (or more than its own advertised TotalSize).
	// TotalSize is read from the first message and validated against the
	// supernode-wide ceiling before any allocation.
	var (
		buf       []byte
		totalSize uint64 // 0 = not yet advertised
		seenFirst bool
	)
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, fmt.Errorf("recv: %w", err)
		}
		if !seenFirst {
			seenFirst = true
			totalSize = msg.TotalSize
			if totalSize > MaxReconstructedBytes {
				return nil, fmt.Errorf("healer advertised total_size=%d exceeds MaxReconstructedBytes=%d", totalSize, MaxReconstructedBytes)
			}
			if totalSize > 0 {
				buf = make([]byte, 0, totalSize)
			}
		}
		// Per-chunk overflow check — works for both bounded (TotalSize>0)
		// and legacy unbounded (TotalSize=0) streams; in the unbounded
		// case we still cap at MaxReconstructedBytes so a stream that
		// "forgets" to advertise size is still safe.
		next := uint64(len(buf)) + uint64(len(msg.Chunk))
		if totalSize > 0 && next > totalSize {
			return nil, fmt.Errorf("healer streamed %d bytes, exceeds advertised total_size=%d", next, totalSize)
		}
		if next > MaxReconstructedBytes {
			return nil, fmt.Errorf("healer streamed %d bytes, exceeds MaxReconstructedBytes=%d", next, MaxReconstructedBytes)
		}
		buf = append(buf, msg.Chunk...)
		if msg.IsLast {
			// Drain any trailer.
			_, _ = stream.Recv()
			if totalSize > 0 && uint64(len(buf)) != totalSize {
				return nil, fmt.Errorf("healer reached IsLast at %d bytes; advertised total_size=%d", len(buf), totalSize)
			}
			return buf, nil
		}
	}
}

// MaxReconstructedBytes caps the verifier-side accumulator for the §19
// healer-served path. Set to 4 GiB which matches typical cascade max-action
// size and bounds the worst-case verifier RAM footprint at runtime. The
// chain-side action enforcement is the authoritative check; this is a
// supernode-side defense-in-depth (H7).
const MaxReconstructedBytes uint64 = 4 * 1024 * 1024 * 1024
