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

	// Channels for lifecycle management
	stopCh chan struct{}
	doneCh chan struct{}
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
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}, nil
}

// GetSupernodeBinary returns the path to the supernode binary
func (m *Manager) GetSupernodeBinary() string {
	// If a specific binary path is configured, use it
	if m.config.SuperNode.BinaryPath != "" {
		return m.config.SuperNode.BinaryPath
	}

	// Otherwise, use the current symlink
	currentLink := filepath.Join(m.homeDir, "current", "supernode")
	if _, err := os.Stat(currentLink); err == nil {
		return currentLink
	}

	// Fallback to system binary
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
	args := []string{"start", "--home", m.config.SuperNode.Home}

	// Add additional args if configured
	if m.config.SuperNode.Args != "" {
		args = append(args, m.config.SuperNode.Args)
	}

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

	// Start monitoring goroutine
	go m.monitor()

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

	timeout := time.Duration(m.config.Manager.ShutdownTimeout) * time.Second
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

// GetStatus returns the current status information
func (m *Manager) GetStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := map[string]interface{}{
		"running":         m.IsRunning(),
		"version":         m.config.Updates.CurrentVersion,
		"manager_version": "dev",
	}

	if m.process != nil {
		status["pid"] = m.process.Pid
		status["uptime"] = time.Since(m.startTime).String()
	}

	return status
}

// monitor watches the process and handles crashes
func (m *Manager) monitor() {
	if m.cmd == nil {
		return
	}

	// Wait for process to exit
	err := m.cmd.Wait()

	m.mu.Lock()
	exitCode := m.cmd.ProcessState.ExitCode()
	m.cleanup()
	m.mu.Unlock()

	// Check if this was an expected shutdown
	select {
	case <-m.stopCh:
		// Expected shutdown
		return
	default:
		// Unexpected exit - this is a crash
		log.Printf("SuperNode crashed with exit code %d: %v", exitCode, err)

		// TODO: Implement restart logic with backoff
		// For now, just log the crash
	}
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

// CheckSupernodeStatus queries the SuperNode's status endpoint
func (m *Manager) CheckSupernodeStatus() (map[string]interface{}, error) {
	// TODO: Call SuperNode's HTTP gateway API at port 8002
	// GET http://localhost:8002/api/status or similar
	return nil, fmt.Errorf("not implemented")
}

// WaitForGracefulShutdown checks if SuperNode has active tasks
func (m *Manager) WaitForGracefulShutdown(timeout time.Duration) error {
	// TODO: Query SuperNode API to check for active tasks
	// Wait for tasks to complete or timeout
	return nil
}
