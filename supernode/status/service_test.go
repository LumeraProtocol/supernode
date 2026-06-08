package status

import (
	"testing"

	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

func TestNewSupernodeStatusService_NilConfigStoragePathsEmpty(t *testing.T) {
	svc := NewSupernodeStatusService(nil, nil, nil, nil)
	if len(svc.storagePaths) != 0 {
		t.Fatalf("expected empty storagePaths for nil config, got %#v", svc.storagePaths)
	}
}

func TestNewSupernodeStatusService_StoragePathsUsesBaseDir(t *testing.T) {
	cfg := &config.Config{BaseDir: "/opt/lumera/.supernode"}
	svc := NewSupernodeStatusService(nil, nil, cfg, nil)
	if len(svc.storagePaths) != 1 || svc.storagePaths[0] != cfg.BaseDir {
		t.Fatalf("unexpected storagePaths: %#v", svc.storagePaths)
	}
}

func TestStatusResponse_ExposesLEP6MetricsSnapshot(t *testing.T) {
	lep6metrics.Reset()
	lep6metrics.IncDispatchResult("PASS")
	lep6metrics.IncHealClaim("submitted")
	lep6metrics.IncHealVerification("submitted", true)
	lep6metrics.IncRecheckSubmission("RECHECK_CONFIRMED_FAIL", "submitted")
	lep6metrics.SetSelfHealingPendingClaims(2)
	t.Cleanup(lep6metrics.Reset)

	svc := NewSupernodeStatusService(nil, nil, nil, nil)
	resp, err := svc.GetStatus(t.Context(), false)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if resp.GetLep6Metrics() == nil {
		t.Fatal("GetStatus() did not include LEP-6 metrics snapshot")
	}
	lep6 := resp.GetLep6Metrics()
	if got := lep6.GetDispatchResultsTotal()["pass"]; got != 1 {
		t.Fatalf("dispatch pass counter = %d, want 1 (all=%#v)", got, lep6.GetDispatchResultsTotal())
	}
	if got := lep6.GetHealClaimsSubmittedTotal()["submitted"]; got != 1 {
		t.Fatalf("heal claim submitted counter = %d, want 1", got)
	}
	if got := lep6.GetHealVerificationsSubmittedTotal()["verified=positive,result=submitted"]; got != 1 {
		t.Fatalf("heal verification submitted counter = %d, want 1", got)
	}
	if got := lep6.GetRecheckEvidenceSubmittedTotal()["class=recheck_confirmed_fail,outcome=submitted"]; got != 1 {
		t.Fatalf("recheck evidence submitted counter = %d, want 1", got)
	}
	if got := lep6.GetSelfHealingPendingClaims(); got != 2 {
		t.Fatalf("self-healing pending claims = %d, want 2", got)
	}
}
