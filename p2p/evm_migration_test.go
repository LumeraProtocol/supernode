package p2p

import "testing"

func TestNotifyEVMMigrationBeforeDHTIsConfiguredMarksPending(t *testing.T) {
	svc := &p2p{}

	svc.NotifyEVMMigration()

	if !svc.migrationPending.Load() {
		t.Fatal("expected migration notification to remain pending until DHT is configured")
	}
}
