package updater

import (
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
)

type fakeGuard struct {
	idle          bool
	err           bool
	restartCalled bool
}

func (f *fakeGuard) isIdle() (bool, bool)            { return f.idle, f.err }
func (f *fakeGuard) requestRestartNow(reason string) { f.restartCalled = true }

// Test busy gateway defers until the max window, then proceeds.
func TestEnsureUpdateWindow_BusyDeferWindow(t *testing.T) {
	cfg := config.DefaultConfig()
	u := New(t.TempDir(), cfg, "1.0.0")

	fg := &fakeGuard{idle: false, err: false}
	u.guard = fg

	// First call: busy and update available -> defer (return false) and set tracking
	if ok := u.ensureUpdateWindow(true, true, "1.2.3"); ok {
		t.Fatalf("expected initial busy defer to return false")
	}
	if u.busyDeferFor != "1.2.3" || u.busyDeferStart.IsZero() {
		t.Fatalf("defer tracking not set")
	}

	// Simulate time passed beyond max window
	u.busyDeferStart = time.Now().Add(-maxBusyDefer - time.Minute)
	if ok := u.ensureUpdateWindow(true, true, "1.2.3"); !ok {
		t.Fatalf("expected proceed after exceeding max defer window")
	}
	if u.busyDeferFor != "" || !u.busyDeferStart.IsZero() {
		t.Fatalf("expected defer tracking cleared")
	}
}

// Test unresponsive gateway: proceed if update exists; request restart otherwise.
func TestEnsureUpdateWindow_Unresponsive(t *testing.T) {
	cfg := config.DefaultConfig()
	u := New(t.TempDir(), cfg, "1.0.0")

	fg := &fakeGuard{idle: false, err: true}
	u.guard = fg

	// Update available -> proceed
	if ok := u.ensureUpdateWindow(true, false, "1.2.3"); !ok {
		t.Fatalf("expected proceed when gateway error and update available")
	}

	// No update -> request restart
	fg.restartCalled = false
	if ok := u.ensureUpdateWindow(false, false, "1.2.3"); ok {
		t.Fatalf("expected no proceed when no update available")
	}
	if !fg.restartCalled {
		t.Fatalf("expected restart marker request on unresponsive with no update")
	}
}
