package host_reporter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	lumeraMock "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	auditmsgmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/audit_msg"
	nodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	supernodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This file guards OPEN-4.
//
// Background: LEP-6 PR #286 deleted the single line that copied the Kademlia
// byte count onto the outgoing HostReport, on the reading that the audit module
// does not consume the value. The value is in fact a metric-COURIER: the audit
// handler bridges it into x/supernode SupernodeMetricsState, which is the only
// source Everlight consults for payout weight and eligibility.
//
// Because the chain-side bridge assigns unconditionally (no zero-guard), a
// daemon that omits the field actively ZEROES a good stored value every epoch.
// On a live devnet a SuperNode holding 611,842 bytes was zeroed within one
// epoch, and at the mainnet floor of 1 GiB no such node can ever earn a payout.
//
// The pre-existing unit tests for cascadeKademliaDBBytes only proved the helper
// computes a number. Nothing asserted the number reached the wire, which is
// exactly why the regression shipped. These tests assert the wire contract.

// writeKademliaStore creates SQLite-shaped files totalling wantBytes and returns
// the directory, mirroring the real p2p data layout (data*.sqlite3 + sidecars).
func writeKademliaStore(t *testing.T, files map[string]int) string {
	t.Helper()

	dir := t.TempDir()
	for name, size := range files {
		payload := make([]byte, size)
		if err := os.WriteFile(filepath.Join(dir, name), payload, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// captureHostReport runs one tick and returns the HostReport actually handed to
// SubmitEpochReport. Asserting on the submitted value (rather than on an
// internal helper) is the whole point: it is the only thing the chain sees.
func captureHostReport(t *testing.T, epochID uint64, p2pDataDir string) audittypes.HostReport {
	t.Helper()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: epochID},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: epochID}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned:       &audittypes.QueryAssignedTargetsResponse{},
	}

	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)

	var captured audittypes.HostReport
	var submitted bool
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), epochID, gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uint64, hr audittypes.HostReport, _ []*audittypes.StorageChallengeObservation, _ []*audittypes.StorageProofResult) (*sdktx.BroadcastTxResponse, error) {
			captured = hr
			submitted = true
			return &sdktx.BroadcastTxResponse{}, nil
		},
	)

	svc, err := NewService(identity, client, kr, keyName, "", p2pDataDir)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.tick(context.Background())

	if !submitted {
		t.Fatal("tick did not submit an epoch report")
	}
	return captured
}

// TestTick_SubmitsCascadeKademliaBytesOnHostReport is the primary regression
// guard: a SuperNode with real Kademlia data must report those bytes on the
// wire. If someone deletes the assignment again, this fails.
func TestTick_SubmitsCascadeKademliaBytesOnHostReport(t *testing.T) {
	const (
		mainDB  = 400_000
		walFile = 180_000
		shmFile = 31_842
	)
	wantBytes := float64(mainDB + walFile + shmFile)

	dir := writeKademliaStore(t, map[string]int{
		"data001.sqlite3":     mainDB,
		"data001.sqlite3-wal": walFile,
		"data001.sqlite3-shm": shmFile,
	})

	got := captureHostReport(t, 21, dir)

	if got.CascadeKademliaDbBytes != wantBytes {
		t.Fatalf("HostReport.CascadeKademliaDbBytes = %v, want %v.\n"+
			"The chain bridges this field into SupernodeMetricsState, which is the "+
			"sole input to Everlight payout weight. Reporting the wrong value (or "+
			"omitting it) silently zeroes the SuperNode's earnings.",
			got.CascadeKademliaDbBytes, wantBytes)
	}
}

// TestTick_CascadeKademliaBytesIsNonZeroWhenStoreHasData states the invariant in
// the form that actually matters economically, independent of the exact byte
// total: a node holding data must never report zero.
//
// This is the assertion that fails on the shipped v2.6.3 daemon.
func TestTick_CascadeKademliaBytesIsNonZeroWhenStoreHasData(t *testing.T) {
	dir := writeKademliaStore(t, map[string]int{
		"data001.sqlite3": 611_842,
	})

	got := captureHostReport(t, 22, dir)

	if got.CascadeKademliaDbBytes <= 0 {
		t.Fatalf("HostReport.CascadeKademliaDbBytes = %v with a non-empty Kademlia "+
			"store; a SuperNode doing real Cascade work must never report zero. "+
			"The chain overwrites stored metrics unconditionally, so a zero here "+
			"destroys accrued Everlight weight every epoch.",
			got.CascadeKademliaDbBytes)
	}
}

// TestTick_CascadeKademliaBytesZeroWhenStoreEmpty pins the honest-zero case, so
// the fix above cannot be "satisfied" by hardcoding a constant. An empty store
// must report 0, not a fabricated value.
func TestTick_CascadeKademliaBytesZeroWhenStoreEmpty(t *testing.T) {
	got := captureHostReport(t, 23, t.TempDir())

	if got.CascadeKademliaDbBytes != 0 {
		t.Fatalf("HostReport.CascadeKademliaDbBytes = %v for an empty Kademlia store, want 0",
			got.CascadeKademliaDbBytes)
	}
}

// TestTick_CascadeKademliaBytesTracksStoreGrowth proves the reported value is
// actually derived from the store rather than any fixed number: growing the
// store must grow the reported bytes.
//
// Without this, a mutation that reports a constant would survive the tests
// above.
func TestTick_CascadeKademliaBytesTracksStoreGrowth(t *testing.T) {
	small := captureHostReport(t, 24, writeKademliaStore(t, map[string]int{
		"data001.sqlite3": 50_000,
	}))
	large := captureHostReport(t, 25, writeKademliaStore(t, map[string]int{
		"data001.sqlite3": 900_000,
	}))

	if !(large.CascadeKademliaDbBytes > small.CascadeKademliaDbBytes) {
		t.Fatalf("reported bytes must track store size: small=%v large=%v; "+
			"a constant or stale value would satisfy the presence checks but is wrong",
			small.CascadeKademliaDbBytes, large.CascadeKademliaDbBytes)
	}
}

// TestTick_CascadeKademliaBytesOmittedWhenNoDataDir documents the deliberate
// exception: a daemon configured without a p2p data directory has nothing to
// measure and reports 0 rather than guessing.
func TestTick_CascadeKademliaBytesOmittedWhenNoDataDir(t *testing.T) {
	got := captureHostReport(t, 26, "")

	if got.CascadeKademliaDbBytes != 0 {
		t.Fatalf("CascadeKademliaDbBytes = %v with no p2p data dir configured, want 0",
			got.CascadeKademliaDbBytes)
	}
}
