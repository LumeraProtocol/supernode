package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the managed SuperNode",
	Long:  `Stop the SuperNode process gracefully.`,
	RunE:  runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	home := getHomeDir()

	// Check PID file
	pidPath := filepath.Join(home, "supernode.pid")
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("SuperNode is not running")
			return nil
		}
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	// Parse PID
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		return fmt.Errorf("invalid PID file: %w", err)
	}

	// Find process
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// Check if process is actually running
	if err := process.Signal(syscall.Signal(0)); err != nil {
		fmt.Println("SuperNode is not running (stale PID)")
		os.Remove(pidPath)
		return nil
	}

	fmt.Printf("Stopping SuperNode (PID %d)...\n", pid)

	// Send SIGTERM for graceful shutdown
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send stop signal: %w", err)
	}

	// Wait for process to exit (with timeout)
	timeout := 30 * time.Second
	checkInterval := 100 * time.Millisecond
	elapsed := time.Duration(0)

	for elapsed < timeout {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			// Process has exited
			fmt.Println("SuperNode stopped successfully")
			os.Remove(pidPath)
			return nil
		}
		time.Sleep(checkInterval)
		elapsed += checkInterval
	}

	// Timeout reached, force kill
	fmt.Println("Graceful shutdown timeout, forcing stop...")
	if err := process.Kill(); err != nil {
		return fmt.Errorf("failed to force stop: %w", err)
	}

	os.Remove(pidPath)
	fmt.Println("SuperNode stopped (forced)")
	return nil
}
