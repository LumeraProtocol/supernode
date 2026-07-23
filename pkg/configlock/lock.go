// Package configlock coordinates SuperNode config writers with sn-manager's
// update preflight. The stable sidecar inode is intentionally retained so all
// processes contend on the same advisory lock.
package configlock

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// Acquire obtains an exclusive advisory lock for configPath. The returned
// release function is idempotent and must be called by the owner.
func Acquire(configPath string) (func() error, error) {
	lockPath := configPath + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock %s: %w", lockPath, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock config %s: %w", configPath, err)
	}

	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
				releaseErr = fmt.Errorf("unlock config %s: %w", configPath, err)
			}
			if err := file.Close(); err != nil && releaseErr == nil {
				releaseErr = fmt.Errorf("close config lock %s: %w", lockPath, err)
			}
		})
		return releaseErr
	}
	return release, nil
}
