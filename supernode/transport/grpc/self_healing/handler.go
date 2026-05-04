// Package self_healing implements the §19 healer-served-path transport.
//
// LEP-6 §19 requires verifiers to fetch reconstructed bytes directly from
// the assigned healer (NOT from KAD), because before chain VERIFIED no copy
// is yet in KAD and the healer is the only authority. This handler exposes
// the verifier-side fetch as a streaming gRPC RPC, gated on caller ∈
// op.VerifierSupernodeAccounts.
package self_healing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// streamChunkBytes is the chunk size used by ServeReconstructedArtefacts.
	// Tuned for grpc max message default (4 MiB) — chunks are 1 MiB so
	// a 100 MiB file streams in ~100 messages.
	streamChunkBytes = 1 << 20
)

// CallerIdentityResolver returns the authenticated chain-side supernode
// account address of the gRPC caller. The production resolver pulls it
// from the secure-rpc / lumeraid handshake the storage_challenge handler
// uses (pkg/reachability.GrpcRemoteIdentityAndAddr).
type CallerIdentityResolver func(ctx context.Context) (string, error)

// DefaultCallerIdentityResolver returns a resolver backed by the secure-rpc
// (Lumera ALTS) handshake. The returned identity is the verifier's
// chain-side supernode account; if the inbound connection is NOT secure-rpc
// the resolver returns an error so the handler refuses to serve.
func DefaultCallerIdentityResolver() CallerIdentityResolver {
	return func(ctx context.Context) (string, error) {
		identity, _ := reachability.GrpcRemoteIdentityAndAddr(ctx)
		identity = strings.TrimSpace(identity)
		if identity == "" {
			return "", errors.New("caller identity unavailable: secure-rpc / ALTS handshake required")
		}
		return identity, nil
	}
}

// Server implements supernode.SelfHealingServiceServer for the LEP-6 §19
// healer-served path. One instance per supernode binary; runs alongside the
// dispatcher Service in self_healing.Service.
type Server struct {
	supernode.UnimplementedSelfHealingServiceServer

	identity      string
	stagingRoot   string
	lumera        lumera.Client
	resolveCaller CallerIdentityResolver
}

// NewServer constructs the §19 transport handler.
//
// resolveCaller authenticates the gRPC peer. Pass DefaultCallerIdentity
// Resolver() in production — it pulls the identity from the secure-rpc
// (Lumera ALTS) handshake. Tests may pass a stub or nil; nil falls back to
// trusting `req.VerifierAccount` (NOT secure — only for unit tests where
// no transport stack is wired up).
func NewServer(identity, stagingRoot string, lumeraClient lumera.Client, resolveCaller CallerIdentityResolver) (*Server, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}
	if lumeraClient == nil || lumeraClient.Audit() == nil {
		return nil, fmt.Errorf("lumera client missing audit module")
	}
	if strings.TrimSpace(stagingRoot) == "" {
		return nil, fmt.Errorf("staging root is empty")
	}
	return &Server{
		identity:      identity,
		stagingRoot:   stagingRoot,
		lumera:        lumeraClient,
		resolveCaller: resolveCaller,
	}, nil
}

// ServeReconstructedArtefacts streams the reconstructed file bytes for one
// heal-op to an authorized verifier.
//
// Authorization (§19): caller must be a member of
// op.VerifierSupernodeAccounts. Caller account is preferentially read from
// CallerIdentityResolver (authenticated transport identity); req.Verifier
// Account is used only as a fallback for tests where no resolver was
// configured — production paths MUST use DefaultCallerIdentityResolver().
func (s *Server) ServeReconstructedArtefacts(req *supernode.ServeReconstructedArtefactsRequest, stream supernode.SelfHealingService_ServeReconstructedArtefactsServer) error {
	if req == nil || req.HealOpId == 0 {
		return status.Error(codes.InvalidArgument, "missing heal_op_id")
	}
	ctx := stream.Context()

	// Resolve caller identity. If a resolver is configured (production),
	// the resolver's verdict wins over req.VerifierAccount — never trust
	// the request payload alone.
	var caller string
	if s.resolveCaller != nil {
		auth, err := s.resolveCaller(ctx)
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "resolve caller: %v", err)
		}
		caller = strings.TrimSpace(auth)
	} else {
		caller = strings.TrimSpace(req.VerifierAccount)
	}
	if caller == "" {
		return status.Error(codes.Unauthenticated, "caller identity unknown")
	}

	// Authorize against on-chain heal-op.
	resp, err := s.lumera.Audit().GetHealOp(ctx, req.HealOpId)
	if err != nil {
		return status.Errorf(codes.NotFound, "heal op %d: %v", req.HealOpId, err)
	}
	if resp == nil {
		return status.Errorf(codes.NotFound, "heal op %d not found", req.HealOpId)
	}
	op := resp.HealOp
	if op.HealerSupernodeAccount != s.identity {
		// Not the assigned healer for this op — refuse to serve so verifiers
		// don't accidentally consult a non-authoritative supernode.
		return status.Error(codes.FailedPrecondition, "this supernode is not the assigned healer for this heal op")
	}
	authorized := false
	for _, v := range op.VerifierSupernodeAccounts {
		if v == caller {
			authorized = true
			break
		}
	}
	if !authorized {
		return status.Errorf(codes.PermissionDenied, "caller %q not in verifier set", caller)
	}

	// Resolve staging dir + reconstructed file.
	stagingDir := filepath.Join(s.stagingRoot, fmt.Sprintf("%d", req.HealOpId))
	info, err := cascadeService.ReadStagedHealOp(stagingDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status.Errorf(codes.NotFound, "no staged heal-op %d", req.HealOpId)
		}
		return status.Errorf(codes.Internal, "read staged heal op: %v", err)
	}

	f, err := os.Open(info.ReconstructedFilePath)
	if err != nil {
		return status.Errorf(codes.Internal, "open staged file: %v", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return status.Errorf(codes.Internal, "stat staged file: %v", err)
	}
	totalSize := uint64(st.Size())

	logtrace.Info(ctx, "self_healing(LEP-6): serving reconstructed artefacts", logtrace.Fields{
		"heal_op_id": req.HealOpId,
		"caller":     caller,
		"size":       totalSize,
	})

	buf := make([]byte, streamChunkBytes)
	first := true
	var sent uint64
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			sent += uint64(n)
			out := &supernode.ServeReconstructedArtefactsResponse{
				Chunk:  append([]byte(nil), buf[:n]...),
				IsLast: false,
			}
			if first {
				out.TotalSize = totalSize
				first = false
			}
			if rerr == io.EOF || sent == totalSize {
				out.IsLast = true
			}
			if err := stream.Send(out); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return status.Errorf(codes.Internal, "read staged file: %v", rerr)
		}
	}
}
