package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop sn-manager and SuperNode",
	Long:  `Stop both the sn-manager daemon and the SuperNode process.`,
	RunE:  runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	home := getHomeDir()

	// Stop the manager (which will handle stopping SuperNode)
	managerPidPath := filepath.Join(home, managerPIDFile)
	mgrPid, err := readPIDFromFile(managerPidPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("sn-manager is not running")
			return nil
		}
		return fmt.Errorf("failed to read manager PID file: %w", err)
	}

	// Find manager process and verify it's alive
	mgrProcess, alive := getProcessIfAlive(mgrPid)
	if !alive {
		fmt.Println("sn-manager is not running (stale PID)")
		if err := os.Remove(managerPidPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove stale manager PID file: %v", err)
		}
		return nil
	}

	fmt.Printf("Stopping sn-manager (PID %d)...\n", mgrPid)

	// Send SIGTERM to manager for graceful shutdown
	if err := mgrProcess.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send stop signal to manager: %w", err)
	}

	// Wait for manager to exit (with timeout)
	timeout := 10 * time.Second
	checkInterval := 100 * time.Millisecond
	elapsed := time.Duration(0)

	for elapsed < timeout {
		if err := mgrProcess.Signal(syscall.Signal(0)); err != nil {
			// Process has exited
			fmt.Println("sn-manager stopped")
			if err := os.Remove(managerPidPath); err != nil && !os.IsNotExist(err) {
				log.Printf("Warning: failed to remove manager PID file: %v", err)
			}
			return nil
		}
		time.Sleep(checkInterval)
		elapsed += checkInterval
	}

	// Timeout reached, force kill
	fmt.Println("Graceful shutdown timeout, forcing manager stop...")
	if err := mgrProcess.Kill(); err != nil {
		return fmt.Errorf("failed to force stop manager: %w", err)
	}

	if err := os.Remove(managerPidPath); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove manager PID file: %v", err)
	}
	fmt.Println("sn-manager stopped (forced)")
	return nil
}
