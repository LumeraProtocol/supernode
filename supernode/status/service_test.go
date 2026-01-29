package status

import (
	"testing"

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
