package manager

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Note: This monitor consumes the update marker ".needs_restart" written by
// the updater after a SuperNode binary update. When present, we perform a
// clean stop and restart to activate the new binary.
//
// Monitor continuously supervises the SuperNode process.
// Steps per cycle:
// 1) Start once if allowed (no stop marker present)
// 2) Wait for process exit and handle restarts unless stop requested
// 3) Periodic checks:
//   - Auto-start if stop marker removed
//   - Restart when update marker is present (".needs_restart" from updater)
//   - Health check for stale processes
func (m *Manager) Monitor(ctx context.Context) error {
	ticker := time.NewTicker(ProcessCheckInterval)
	defer ticker.Stop()

	processExitCh := make(chan error, 1)

	// Step 1: Initial start
	m.initialStartIfAllowed(ctx, &processExitCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-processExitCh:
			// Step 2: Handle process exit
			m.handleProcessExit(ctx, err, &processExitCh)
		case <-ticker.C:
			// Step 3: Periodic checks
			m.periodicChecks(ctx, &processExitCh)
		}
	}
}

// initialStartIfAllowed starts SuperNode if there's no stop marker, and arms the wait.
func (m *Manager) initialStartIfAllowed(ctx context.Context, processExitCh *chan error) {
	stopMarkerPath := filepath.Join(m.homeDir, StopMarkerFile)
	if _, err := os.Stat(stopMarkerPath); os.IsNotExist(err) {
		if !m.IsRunning() {
			log.Println("Starting SuperNode...")
			if err := m.Start(ctx); err != nil {
				log.Printf("Failed to start SuperNode: %v", err)
				return
			}
			m.armProcessWait(processExitCh)
		} else {
			m.armProcessWait(processExitCh)
		}
	} else {
		log.Println("Stop marker present, SuperNode will not be started")
	}
}

// armProcessWait launches a goroutine that waits on the process and reports the result.
func (m *Manager) armProcessWait(processExitCh *chan error) {
	ch := make(chan error, 1)
	*processExitCh = ch
	go func() {
		if err := m.Wait(); err != nil {
			ch <- err
		} else {
			ch <- nil
		}
	}()
}

// handleProcessExit cleans up state and restarts unless a stop marker is present.
func (m *Manager) handleProcessExit(ctx context.Context, err error, processExitCh *chan error) {
	if err != nil {
		log.Printf("SuperNode exited with error: %v", err)
	} else {
		log.Printf("SuperNode exited normally")
	}

	m.mu.Lock()
	m.cleanup()
	m.mu.Unlock()

	stopMarkerPath := filepath.Join(m.homeDir, StopMarkerFile)
	if _, err := os.Stat(stopMarkerPath); err == nil {
		log.Println("Stop marker present, not restarting SuperNode")
		return
	}

	time.Sleep(CrashBackoffDelay)
	log.Println("Restarting SuperNode after crash...")
	if err := m.Start(ctx); err != nil {
		log.Printf("Failed to restart SuperNode: %v", err)
		return
	}
	m.armProcessWait(processExitCh)
	log.Println("SuperNode restarted successfully")
}

// periodicChecks performs periodic supervision tasks:
// - Start on stop-marker removal
// - Restart on update-marker
// - Process liveness check
func (m *Manager) periodicChecks(ctx context.Context, processExitCh *chan error) {
	stopMarkerPath := filepath.Join(m.homeDir, StopMarkerFile)
	if !m.IsRunning() {
		if _, err := os.Stat(stopMarkerPath); os.IsNotExist(err) {
			log.Println("Stop marker removed, starting SuperNode...")
			if err := m.Start(ctx); err != nil {
				log.Printf("Failed to start SuperNode: %v", err)
			} else {
				m.armProcessWait(processExitCh)
				log.Println("SuperNode started")
			}
		}
	}

	// Restart when updated binary is ready
	restartMarkerPath := filepath.Join(m.homeDir, RestartMarkerFile)
	if _, err := os.Stat(restartMarkerPath); err == nil {
		if m.IsRunning() {
			log.Println("Binary update detected, restarting SuperNode...")
			if err := os.Remove(restartMarkerPath); err != nil && !os.IsNotExist(err) {
				log.Printf("Warning: failed to remove restart marker: %v", err)
			}
			// Temporary stop marker for clean restart
			_ = os.WriteFile(stopMarkerPath, []byte("update"), 0644)
			if err := m.Stop(); err != nil {
				log.Printf("Failed to stop for update: %v", err)
				if err := os.Remove(stopMarkerPath); err != nil && !os.IsNotExist(err) {
					log.Printf("Warning: failed to remove stop marker: %v", err)
				}
				return
			}
			time.Sleep(CrashBackoffDelay)
			if err := os.Remove(stopMarkerPath); err != nil && !os.IsNotExist(err) {
				log.Printf("Warning: failed to remove stop marker: %v", err)
			}
			log.Println("Starting with updated binary...")
			if err := m.Start(ctx); err != nil {
				log.Printf("Failed to start updated binary: %v", err)
			} else {
				m.armProcessWait(processExitCh)
				log.Println("SuperNode restarted with new binary")
			}
		}
	}

	// Health check
	if m.IsRunning() {
		m.mu.RLock()
		proc := m.process
		m.mu.RUnlock()
		if proc != nil {
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				log.Println("Detected stale process, cleaning up...")
				m.mu.Lock()
				m.cleanup()
				m.mu.Unlock()
			}
		}
	}
}
