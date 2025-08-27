package manager

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
)

// Manager handles the SuperNode process lifecycle
type Manager struct {
	config    *config.Config
	homeDir   string
	process   *os.Process
	cmd       *exec.Cmd
	mu        sync.RWMutex
	startTime time.Time
}

// New creates a new Manager instance
func New(homeDir string) (*Manager, error) {
	// Load configuration
	configPath := filepath.Join(homeDir, "config.yml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &Manager{
		config:  cfg,
		homeDir: homeDir,
	}, nil
}

// GetSupernodeBinary returns the path to the supernode binary
func (m *Manager) GetSupernodeBinary() string {
	// Use the current symlink managed by sn-manager
	currentLink := filepath.Join(m.homeDir, "current", "supernode")
	if _, err := os.Stat(currentLink); err == nil {
		return currentLink
	}

	// Fallback to system binary if no managed version exists
	return "supernode"
}

// Start launches the SuperNode process
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.process != nil {
		return fmt.Errorf("supernode is already running")
	}

	// Prepare command
	binary := m.GetSupernodeBinary()
	// SuperNode will handle its own home directory and arguments
	args := []string{"start"}

	m.cmd = exec.CommandContext(ctx, binary, args...)
	m.cmd.Stdout = os.Stdout
	m.cmd.Stderr = os.Stderr

	// Start the process
	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start supernode: %w", err)
	}

	m.process = m.cmd.Process
	m.startTime = time.Now()

	// Save PID
	pidPath := filepath.Join(m.homeDir, "supernode.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", m.process.Pid)), 0644); err != nil {
		log.Printf("Warning: failed to save PID file: %v", err)
	}

	log.Printf("SuperNode started with PID %d", m.process.Pid)
	return nil
}

// Stop gracefully stops the SuperNode process
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.process == nil {
		return fmt.Errorf("supernode is not running")
	}

	log.Printf("Stopping SuperNode (PID %d)...", m.process.Pid)

	// Send SIGTERM for graceful shutdown
	if err := m.process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	// Poll for graceful shutdown with timeout without calling Wait to avoid double-wait
	timeout := DefaultShutdownTimeout
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := m.process.Signal(syscall.Signal(0)); err != nil {
			// Process no longer exists
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// If still alive, force kill
	if err := m.process.Signal(syscall.Signal(0)); err == nil {
		log.Printf("Graceful shutdown timeout, forcing kill...")
		if err := m.process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	// Cleanup
	m.cleanup()
	log.Printf("SuperNode stopped")
	return nil
}

// IsRunning checks if the SuperNode process is running
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.process == nil {
		return false
	}

	// Check if process still exists
	err := m.process.Signal(syscall.Signal(0))
	return err == nil
}

// Wait waits for the SuperNode process to exit and returns any error
func (m *Manager) Wait() error {
	m.mu.RLock()
	cmd := m.cmd
	m.mu.RUnlock()

	if cmd == nil {
		return fmt.Errorf("no process running")
	}

	return cmd.Wait()
}

// cleanup performs cleanup after process stops
func (m *Manager) cleanup() {
	m.process = nil
	m.cmd = nil

	// Remove PID file
	pidPath := filepath.Join(m.homeDir, "supernode.pid")
	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove PID file: %v", err)
	}
}

// Constants for process management
const (
	DefaultShutdownTimeout = 30 * time.Second
	ProcessCheckInterval   = 5 * time.Second
	CrashBackoffDelay     = 2 * time.Second
	StopMarkerFile        = ".stop_requested"
	RestartMarkerFile     = ".needs_restart"
)

// Monitor continuously supervises the SuperNode process
// It ensures SuperNode is always running unless a stop marker is present
func (m *Manager) Monitor(ctx context.Context) error {

	// Create ticker for periodic checks
	ticker := time.NewTicker(ProcessCheckInterval)
	defer ticker.Stop()

	// Channel to monitor process exits
	processExitCh := make(chan error, 1)
	
	// Function to arm the process wait goroutine
	armProcessWait := func() {
		processExitCh = make(chan error, 1)
		go func() {
			if err := m.Wait(); err != nil {
				processExitCh <- err
			} else {
				processExitCh <- nil
			}
		}()
	}

	// Initial check and start if needed
	stopMarkerPath := filepath.Join(m.homeDir, StopMarkerFile)
	if _, err := os.Stat(stopMarkerPath); os.IsNotExist(err) {
		// No stop marker, ensure SuperNode is running
		if !m.IsRunning() {
			log.Println("Starting SuperNode...")
			if err := m.Start(ctx); err != nil {
				log.Printf("Failed to start SuperNode: %v", err)
			} else {
				armProcessWait()
			}
		} else {
			// Already running, arm the wait
			armProcessWait()
		}
	} else {
		log.Println("Stop marker present, SuperNode will not be started")
	}

	// Main supervision loop
	for {
		select {
		case <-ctx.Done():
			// Context cancelled, stop monitoring
			return ctx.Err()

		case err := <-processExitCh:
			// SuperNode process exited
			if err != nil {
				log.Printf("SuperNode exited with error: %v", err)
			} else {
				log.Printf("SuperNode exited normally")
			}

			// Cleanup internal state after exit
			m.mu.Lock()
			m.cleanup()
			m.mu.Unlock()

			// Check if we should restart
			if _, err := os.Stat(stopMarkerPath); err == nil {
				log.Println("Stop marker present, not restarting SuperNode")
				continue
			}

			// Apply backoff to prevent rapid restart loops
			time.Sleep(CrashBackoffDelay)

			// Restart SuperNode
			log.Println("Restarting SuperNode after crash...")
			if err := m.Start(ctx); err != nil {
				log.Printf("Failed to restart SuperNode: %v", err)
				continue
			}
			armProcessWait()
			log.Println("SuperNode restarted successfully")

		case <-ticker.C:
			// Periodic check for various conditions
			
			// 1. Check if stop marker was removed and we should start
			if !m.IsRunning() {
				if _, err := os.Stat(stopMarkerPath); os.IsNotExist(err) {
					log.Println("Stop marker removed, starting SuperNode...")
					if err := m.Start(ctx); err != nil {
						log.Printf("Failed to start SuperNode: %v", err)
					} else {
						armProcessWait()
						log.Println("SuperNode started")
					}
				}
			}

			// 2. Check if binary was updated and needs restart
			restartMarkerPath := filepath.Join(m.homeDir, RestartMarkerFile)
			if _, err := os.Stat(restartMarkerPath); err == nil {
				if m.IsRunning() {
					log.Println("Binary update detected, restarting SuperNode...")
					
					// Remove the restart marker
					if err := os.Remove(restartMarkerPath); err != nil && !os.IsNotExist(err) {
						log.Printf("Warning: failed to remove restart marker: %v", err)
					}
					
					// Create temporary stop marker for clean restart
					tmpStopMarker := []byte("update")
					os.WriteFile(stopMarkerPath, tmpStopMarker, 0644)
					
					// Stop current process
					if err := m.Stop(); err != nil {
						log.Printf("Failed to stop for update: %v", err)
						if err := os.Remove(stopMarkerPath); err != nil && !os.IsNotExist(err) {
							log.Printf("Warning: failed to remove stop marker: %v", err)
						}
						continue
					}
					
					// Brief pause
					time.Sleep(CrashBackoffDelay)
					
					// Remove temporary stop marker
					if err := os.Remove(stopMarkerPath); err != nil && !os.IsNotExist(err) {
						log.Printf("Warning: failed to remove stop marker: %v", err)
					}
					
					// Start with new binary
					log.Println("Starting with updated binary...")
					if err := m.Start(ctx); err != nil {
						log.Printf("Failed to start updated binary: %v", err)
					} else {
						armProcessWait()
						log.Println("SuperNode restarted with new binary")
					}
				}
			}

			// 3. Health check - ensure process is actually alive
			if m.IsRunning() {
				// Process thinks it's running, verify it really is
				m.mu.RLock()
				proc := m.process
				m.mu.RUnlock()
				
				if proc != nil {
					if err := proc.Signal(syscall.Signal(0)); err != nil {
						// Process is dead but not cleaned up
						log.Println("Detected stale process, cleaning up...")
						m.mu.Lock()
						m.cleanup()
						m.mu.Unlock()
					}
				}
			}
		}
	}
}

// GetConfig returns the manager configuration
func (m *Manager) GetConfig() *config.Config {
	return m.config
}

