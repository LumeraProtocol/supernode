package host_reporter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	lumeraMock "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	supernodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"go.uber.org/mock/gomock"
)

func TestNormalizeProbeHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ipv4 host only", in: "203.0.113.1", want: "203.0.113.1"},
		{name: "host port", in: "example.com:8080", want: "example.com"},
		{name: "ipv6 host only", in: "2001:db8::1", want: "2001:db8::1"},
		{name: "bracketed ipv6 host only", in: "[2001:db8::1]", want: "2001:db8::1"},
		{name: "bracketed ipv6 host port", in: "[2001:db8::1]:8080", want: "2001:db8::1"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeProbeHost(tc.in); got != tc.want {
				t.Fatalf("normalizeProbeHost(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCascadeKademliaDBBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite := func(name string, size int) {
		p := filepath.Join(dir, name)
		b := make([]byte, size)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	mustWrite("data001.sqlite3", 100)
	mustWrite("data001.sqlite3-wal", 50)
	mustWrite("data001.sqlite3-shm", 25)
	mustWrite("unrelated.txt", 999)

	s := &Service{p2pDataDir: dir}
	got, ok := s.cascadeKademliaDBBytes(context.Background())
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if want := uint64(175); got != want {
		t.Fatalf("cascadeKademliaDBBytes=%d want %d", got, want)
	}
}

func TestCascadeKademliaDBBytes_NoMatches(t *testing.T) {
	t.Parallel()
	s := &Service{p2pDataDir: t.TempDir()}
	_, ok := s.cascadeKademliaDBBytes(context.Background())
	if ok {
		t.Fatalf("expected ok=false when no sqlite db files exist")
	}
}

func TestAuditDiskUsagePercentCompat(t *testing.T) {
	tests := []struct {
		name       string
		actual     float64
		state      sntypes.SuperNodeState
		reason     string
		want       float64
		wantReason string
	}{
		{name: "active below audit threshold reports actual", actual: 84.9, state: sntypes.SuperNodeStateActive, want: 84.9},
		{name: "active at audit threshold reports storage full signal", actual: 85, state: sntypes.SuperNodeStateActive, want: 90.00000000000001, wantReason: "storage_full_compat"},
		{name: "storage full in overlap stays storage full", actual: 87, state: sntypes.SuperNodeStateStorageFull, want: 90.00000000000001, wantReason: "storage_full_compat"},
		{name: "postponed host requirements reports recovery value", actual: 88, state: sntypes.SuperNodeStatePostponed, reason: postponeReasonAuditHostRequirements, want: 85, wantReason: "postponed_recovery_compat"},
		{name: "postponed old no-reason reports recovery value", actual: 88, state: sntypes.SuperNodeStatePostponed, want: 85, wantReason: "postponed_recovery_compat"},
		{name: "postponed non-host reason reports actual", actual: 88, state: sntypes.SuperNodeStatePostponed, reason: "audit_peer_ports", want: 88},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			auditMod := &stubAuditModule{params: audittypes.Params{MinDiskFreePercent: 15}}
			snMod := supernodemod.NewMockModule(ctrl)
			client := lumeraMock.NewMockClient(ctrl)
			client.EXPECT().Audit().Return(auditMod)
			client.EXPECT().SuperNode().Return(snMod).AnyTimes()
			snMod.EXPECT().GetParams(gomock.Any()).Return(&sntypes.QueryParamsResponse{Params: sntypes.Params{MaxStorageUsagePercent: 90}}, nil)
			if tc.wantReason != "" || tc.state == sntypes.SuperNodeStatePostponed {
				snMod.EXPECT().GetSupernodeBySupernodeAddress(gomock.Any(), "local-sn").Return(&sntypes.SuperNode{
					SupernodeAccount: "local-sn",
					States: []*sntypes.SuperNodeStateRecord{{
						State:  tc.state,
						Height: 1,
						Reason: tc.reason,
					}},
				}, nil)
			}

			svc := &Service{identity: "local-sn", lumera: client}
			got, reason := svc.auditDiskUsagePercent(context.Background(), tc.actual)
			if got != tc.want {
				t.Fatalf("reported disk=%v want %v", got, tc.want)
			}
			if reason != tc.wantReason {
				t.Fatalf("reason=%q want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestAuditDiskUsagePercentCompatFailsClosedWhenParamsUnavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	auditMod := &stubAuditModule{params: audittypes.Params{MinDiskFreePercent: 0}}
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().Return(auditMod)

	svc := &Service{identity: "local-sn", lumera: client}
	got, reason := svc.auditDiskUsagePercent(context.Background(), 88)
	if got != 88 {
		t.Fatalf("reported disk=%v want actual", got)
	}
	if reason != "" {
		t.Fatalf("reason=%q want empty", reason)
	}
}
