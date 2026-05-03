package self_healing

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/audit"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/audit_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	bankmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/bank"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	supernodeMod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/LumeraProtocol/supernode/v2/pkg/testutil"
	query "github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// ---------------------------------------------------------------------------
// Test 4 — TestServeReconstructedArtefacts_AuthorizesOnlyAssignedVerifiers.
// ---------------------------------------------------------------------------
func TestServeReconstructedArtefacts_AuthorizesOnlyAssignedVerifiers(t *testing.T) {
	srv, cleanup, _ := newHandlerHarness(t, "sn-healer", &handlerOp{
		HealOpId:                  100,
		HealerSupernodeAccount:    "sn-healer",
		VerifierSupernodeAccounts: []string{"sn-v1", "sn-v2"},
	}, []byte("payload-bytes"))
	defer cleanup()

	body, err := callServe(t, srv, &supernode.ServeReconstructedArtefactsRequest{
		HealOpId:        100,
		VerifierAccount: "sn-v1",
	})
	if err != nil {
		t.Fatalf("authorized verifier should succeed: %v", err)
	}
	if string(body) != "payload-bytes" {
		t.Fatalf("unexpected body: %q", string(body))
	}
}

// ---------------------------------------------------------------------------
// Test 5 — TestServeReconstructedArtefacts_RejectsUnassignedCaller.
// ---------------------------------------------------------------------------
func TestServeReconstructedArtefacts_RejectsUnassignedCaller(t *testing.T) {
	srv, cleanup, _ := newHandlerHarness(t, "sn-healer", &handlerOp{
		HealOpId:                  101,
		HealerSupernodeAccount:    "sn-healer",
		VerifierSupernodeAccounts: []string{"sn-v1", "sn-v2"},
	}, []byte("p"))
	defer cleanup()

	_, err := callServe(t, srv, &supernode.ServeReconstructedArtefactsRequest{
		HealOpId:        101,
		VerifierAccount: "sn-attacker",
	})
	if err == nil {
		t.Fatalf("unauthorized caller must be rejected")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v: %v", st.Code(), err)
	}

	// Also: a different supernode that isn't even the assigned healer should
	// refuse to serve regardless of caller.
	wrongHealerSrv, wrongCleanup, _ := newHandlerHarness(t, "sn-not-healer", &handlerOp{
		HealOpId:                  102,
		HealerSupernodeAccount:    "sn-real-healer",
		VerifierSupernodeAccounts: []string{"sn-v1"},
	}, []byte("p"))
	defer wrongCleanup()
	_, err = callServe(t, wrongHealerSrv, &supernode.ServeReconstructedArtefactsRequest{
		HealOpId:        102,
		VerifierAccount: "sn-v1",
	})
	if err == nil {
		t.Fatalf("non-assigned-healer must refuse to serve")
	}
	st, _ = status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v: %v", st.Code(), err)
	}
}

// ---------------------------------------------------------------------------
// handler harness
// ---------------------------------------------------------------------------

type handlerOp struct {
	HealOpId                  uint64
	HealerSupernodeAccount    string
	VerifierSupernodeAccounts []string
}

func newHandlerHarness(t *testing.T, identity string, op *handlerOp, body []byte) (*Server, func(), string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "heal-staging")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	hash, err := cascadekit.ComputeBlake3DataHashB64(body)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	dir := makeStagingDir(t, root, op.HealOpId, hash, body)

	a := &handlerStubAudit{op: audittypes.HealOp{
		HealOpId:                  op.HealOpId,
		HealerSupernodeAccount:    op.HealerSupernodeAccount,
		VerifierSupernodeAccounts: op.VerifierSupernodeAccounts,
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		ResultHash:                hash,
	}}
	srv, err := NewServer(identity, root, &handlerLumera{audit: a}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return srv, cleanup, hash
}

// callServe dials the server through bufconn and consumes the stream.
func callServe(t *testing.T, srv *Server, req *supernode.ServeReconstructedArtefactsRequest) ([]byte, error) {
	t.Helper()
	listener := bufconn.Listen(1 << 16)
	gs := grpc.NewServer()
	supernode.RegisterSelfHealingServiceServer(gs, srv)
	go func() { _ = gs.Serve(listener) }()
	defer gs.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := supernode.NewSelfHealingServiceClient(conn)
	stream, err := c.ServeReconstructedArtefacts(ctx, req)
	if err != nil {
		return nil, err
	}
	var buf []byte
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
		buf = append(buf, msg.Chunk...)
		if msg.IsLast {
			// Drain to surface a trailing status (if any).
			_, _ = stream.Recv()
			return buf, nil
		}
	}
}

// handlerLumera is a minimal lumera.Client for the transport handler tests
// — only Audit() is consulted.
type handlerLumera struct {
	mu       sync.Mutex
	audit    audit.Module
	stubsRef lumera.Client
}

func (h *handlerLumera) Auth() auth.Module {
	h.ensureStubs()
	return h.stubsRef.Auth()
}
func (h *handlerLumera) Action() action.Module {
	h.ensureStubs()
	return h.stubsRef.Action()
}
func (h *handlerLumera) ActionMsg() action_msg.Module {
	h.ensureStubs()
	return h.stubsRef.ActionMsg()
}
func (h *handlerLumera) Audit() audit.Module        { return h.audit }
func (h *handlerLumera) AuditMsg() audit_msg.Module { return h.stubsRef.AuditMsg() }
func (h *handlerLumera) SuperNode() supernodeMod.Module {
	h.ensureStubs()
	return h.stubsRef.SuperNode()
}
func (h *handlerLumera) SuperNodeMsg() supernode_msg.Module {
	h.ensureStubs()
	return h.stubsRef.SuperNodeMsg()
}
func (h *handlerLumera) Bank() bankmod.Module {
	h.ensureStubs()
	return h.stubsRef.Bank()
}
func (h *handlerLumera) Tx() tx.Module {
	h.ensureStubs()
	return h.stubsRef.Tx()
}
func (h *handlerLumera) Node() node.Module {
	h.ensureStubs()
	return h.stubsRef.Node()
}
func (h *handlerLumera) Close() error { return nil }

func (h *handlerLumera) ensureStubs() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stubsRef == nil {
		c, err := testutil.NewMockLumeraClient(nil, nil)
		if err != nil {
			panic(err)
		}
		h.stubsRef = c
	}
}

type handlerStubAudit struct{ op audittypes.HealOp }

func (h *handlerStubAudit) GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error) {
	return &audittypes.QueryParamsResponse{}, nil
}
func (h *handlerStubAudit) GetEpochAnchor(ctx context.Context, epochID uint64) (*audittypes.QueryEpochAnchorResponse, error) {
	return &audittypes.QueryEpochAnchorResponse{}, nil
}
func (h *handlerStubAudit) GetCurrentEpochAnchor(ctx context.Context) (*audittypes.QueryCurrentEpochAnchorResponse, error) {
	return &audittypes.QueryCurrentEpochAnchorResponse{}, nil
}
func (h *handlerStubAudit) GetCurrentEpoch(ctx context.Context) (*audittypes.QueryCurrentEpochResponse, error) {
	return &audittypes.QueryCurrentEpochResponse{}, nil
}
func (h *handlerStubAudit) GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*audittypes.QueryAssignedTargetsResponse, error) {
	return &audittypes.QueryAssignedTargetsResponse{}, nil
}
func (h *handlerStubAudit) GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*audittypes.QueryEpochReportResponse, error) {
	return &audittypes.QueryEpochReportResponse{}, nil
}
func (h *handlerStubAudit) GetEpochReportsByReporter(ctx context.Context, reporterAccount string, epochID uint64) (*audittypes.QueryEpochReportsByReporterResponse, error) {
	return &audittypes.QueryEpochReportsByReporterResponse{}, nil
}
func (h *handlerStubAudit) GetNodeSuspicionState(ctx context.Context, supernodeAccount string) (*audittypes.QueryNodeSuspicionStateResponse, error) {
	return &audittypes.QueryNodeSuspicionStateResponse{}, nil
}
func (h *handlerStubAudit) GetReporterReliabilityState(ctx context.Context, reporterAccount string) (*audittypes.QueryReporterReliabilityStateResponse, error) {
	return &audittypes.QueryReporterReliabilityStateResponse{}, nil
}
func (h *handlerStubAudit) GetTicketDeteriorationState(ctx context.Context, ticketID string) (*audittypes.QueryTicketDeteriorationStateResponse, error) {
	return &audittypes.QueryTicketDeteriorationStateResponse{}, nil
}
func (h *handlerStubAudit) GetHealOp(ctx context.Context, healOpID uint64) (*audittypes.QueryHealOpResponse, error) {
	if healOpID != h.op.HealOpId {
		return nil, errors.New("not found")
	}
	return &audittypes.QueryHealOpResponse{HealOp: h.op}, nil
}
func (h *handlerStubAudit) GetHealOpsByStatus(ctx context.Context, status audittypes.HealOpStatus, pagination *query.PageRequest) (*audittypes.QueryHealOpsByStatusResponse, error) {
	return &audittypes.QueryHealOpsByStatusResponse{}, nil
}
func (h *handlerStubAudit) GetHealOpsByTicket(ctx context.Context, ticketID string, pagination *query.PageRequest) (*audittypes.QueryHealOpsByTicketResponse, error) {
	return &audittypes.QueryHealOpsByTicketResponse{}, nil
}
