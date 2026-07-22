package configlock

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireSerializesConfigWriters(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yml")
	releaseFirst, err := Acquire(configPath)
	if err != nil {
		t.Fatal(err)
	}

	acquiredSecond := make(chan func() error, 1)
	errSecond := make(chan error, 1)
	go func() {
		release, err := Acquire(configPath)
		if err != nil {
			errSecond <- err
			return
		}
		acquiredSecond <- release
	}()

	select {
	case <-acquiredSecond:
		t.Fatal("second writer acquired lock before first released it")
	case err := <-errSecond:
		t.Fatalf("second writer failed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := releaseFirst(); err != nil {
		t.Fatal(err)
	}
	select {
	case releaseSecond := <-acquiredSecond:
		if err := releaseSecond(); err != nil {
			t.Fatal(err)
		}
	case err := <-errSecond:
		t.Fatalf("second writer failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second writer did not acquire lock after release")
	}
}
