package cmd

import (
    "fmt"
    "log"
    "os"
    "path/filepath"
    "syscall"
    "time"
    
    "github.com/LumeraProtocol/supernode/v2/sn-manager/internal/manager"
    "github.com/spf13/cobra"
)

// supernodeStopCmd stops only SuperNode (not the manager)
var supernodeStopCmd = &cobra.Command{
    Use:   "stop",
    Short: "Stop the managed SuperNode",
    Long:  `Gracefully stop the managed SuperNode and prevent auto-restart until started again.`,
    RunE:  runSupernodeStop,
}

func runSupernodeStop(cmd *cobra.Command, args []string) error {
    home := getHomeDir()

    // Check PID file
    pidPath := filepath.Join(home, supernodePIDFile)
    pid, err := readPIDFromFile(pidPath)
    if err != nil {
        if os.IsNotExist(err) {
            fmt.Println("SuperNode is not running")
            return nil
        }
        return fmt.Errorf("failed to read PID file: %w", err)
    }

    // Find process and verify it's alive
    process, alive := getProcessIfAlive(pid)
    if !alive {
        fmt.Println("SuperNode is not running (stale PID)")
        if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
            log.Printf("Warning: failed to remove stale PID file: %v", err)
        }
        return nil
    }

    fmt.Printf("Stopping SuperNode (PID %d)...\n", pid)

    // Write stop marker so manager does not auto-restart
    stopMarker := filepath.Join(home, stopMarkerFile)
    _ = os.WriteFile(stopMarker, []byte("1"), 0644)

    // Send SIGTERM for graceful shutdown
    if err := process.Signal(syscall.SIGTERM); err != nil {
        return fmt.Errorf("failed to send stop signal: %w", err)
    }

    // Wait for process to exit (with timeout)
    timeout := manager.DefaultShutdownTimeout
    checkInterval := 100 * time.Millisecond
    elapsed := time.Duration(0)

    for elapsed < timeout {
        if err := process.Signal(syscall.Signal(0)); err != nil {
            // Process has exited
            fmt.Println("SuperNode stopped successfully")
            if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
                log.Printf("Warning: failed to remove PID file: %v", err)
            }
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

    if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
        log.Printf("Warning: failed to remove PID file: %v", err)
    }
    fmt.Println("SuperNode stopped (forced)")
    return nil
}

