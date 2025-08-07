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

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
)

// Manager handles the SuperNode process lifecycle
type Manager struct {
	config    *config.Config
	homeDir   string
	process   *os.Process
	cmd       *exec.Cmd
	mu        sync.RWMutex
	logFile   *os.File
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

	// Open log file
	logPath := filepath.Join(m.homeDir, "logs", "supernode.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	m.logFile = logFile

	// Prepare command
	binary := m.GetSupernodeBinary()
	// SuperNode will handle its own home directory and arguments
	args := []string{"start"}

	log.Printf("Starting SuperNode: %s %v", binary, args)

	m.cmd = exec.CommandContext(ctx, binary, args...)
	m.cmd.Stdout = m.logFile
	m.cmd.Stderr = m.logFile

	// Start the process
	if err := m.cmd.Start(); err != nil {
		m.logFile.Close()
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

	// Wait for graceful shutdown with timeout
	done := make(chan error, 1)
	go func() {
		_, err := m.process.Wait()
		done <- err
	}()

	timeout := 30 * time.Second // Default shutdown timeout
	select {
	case <-time.After(timeout):
		log.Printf("Graceful shutdown timeout, forcing kill...")
		if err := m.process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
		<-done
	case err := <-done:
		if err != nil && err.Error() != "signal: terminated" {
			log.Printf("Process exited with error: %v", err)
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

// cleanup performs cleanup after process stops
func (m *Manager) cleanup() {
	m.process = nil
	m.cmd = nil

	if m.logFile != nil {
		m.logFile.Close()
		m.logFile = nil
	}

	// Remove PID file
	pidPath := filepath.Join(m.homeDir, "supernode.pid")
	os.Remove(pidPath)
}
