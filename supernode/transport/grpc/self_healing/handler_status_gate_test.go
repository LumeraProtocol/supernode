package self_healing

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	cascadekit "github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statusOverrideAudit lets a test inject a chosen HealOp status while keeping
// the rest of the wiring identical to handlerStubAudit.
type statusOverrideAudit struct {
	handlerStubAudit
}

func newStatusHarness(t *testing.T, healStatus audittypes.HealOpStatus, body []byte) (*Server, func(), uint64) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "heal-staging")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	hash, err := cascadekit.ComputeBlake3DataHashB64(body)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	const healOpID = 555
	dir := makeStagingDir(t, root, healOpID, hash, body)

	a := &statusOverrideAudit{handlerStubAudit: handlerStubAudit{op: audittypes.HealOp{
		HealOpId:                  healOpID,
		HealerSupernodeAccount:    "sn-healer",
		VerifierSupernodeAccounts: []string{"sn-v1"},
		Status:                    healStatus,
		ResultHash:                hash,
	}}}
	srv, err := NewServerForTest("sn-healer", root, &handlerLumera{audit: a}, nil)
	if err != nil {
		t.Fatalf("NewServerForTest: %v", err)
	}
	return srv, func() { _ = os.RemoveAll(dir) }, healOpID
}

// TestServeReconstructedArtefacts_StatusGate verifies H8: serve is only valid
// while op.Status == HEALER_REPORTED. All other statuses must be rejected
// with FailedPrecondition.
func TestServeReconstructedArtefacts_StatusGate(t *testing.T) {
	body := []byte("payload")
	disallowed := []audittypes.HealOpStatus{
		audittypes.HealOpStatus_HEAL_OP_STATUS_UNSPECIFIED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED,
	}
	for _, st := range disallowed {
		st := st
		t.Run(st.String(), func(t *testing.T) {
			srv, cleanup, opID := newStatusHarness(t, st, body)
			defer cleanup()
			_, err := callServe(t, srv, &supernode.ServeReconstructedArtefactsRequest{
				HealOpId:        opID,
				VerifierAccount: "sn-v1",
			})
			if err == nil {
				t.Fatalf("status %s must be rejected", st)
			}
			s, _ := status.FromError(err)
			if s.Code() != codes.FailedPrecondition {
				t.Fatalf("status %s: expected FailedPrecondition, got %v: %v", st, s.Code(), err)
			}
		})
	}

	// Sanity: HEALER_REPORTED still works.
	srv, cleanup, opID := newStatusHarness(t, audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED, body)
	defer cleanup()
	got, err := callServe(t, srv, &supernode.ServeReconstructedArtefactsRequest{
		HealOpId:        opID,
		VerifierAccount: "sn-v1",
	})
	if err != nil {
		t.Fatalf("HEALER_REPORTED must succeed: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q, want %q", got, body)
	}
}

// TestNewServer_RejectsNilResolver verifies L1: production constructor
// rejects a nil resolveCaller — only NewServerForTest may accept nil.
func TestNewServer_RejectsNilResolver(t *testing.T) {
	a := &handlerStubAudit{op: audittypes.HealOp{HealOpId: 1}}
	_, err := NewServer("sn-x", t.TempDir(), &handlerLumera{audit: a}, nil)
	if err == nil {
		t.Fatalf("expected error from NewServer with nil resolver")
	}

	// Test-only constructor must still accept nil (it's the documented escape hatch).
	if _, err := NewServerForTest("sn-x", t.TempDir(), &handlerLumera{audit: a}, nil); err != nil {
		t.Fatalf("NewServerForTest must accept nil: %v", err)
	}
}
