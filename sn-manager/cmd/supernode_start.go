package cmd

import (
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"

    "github.com/spf13/cobra"
)

var supernodeStartCmd = &cobra.Command{
    Use:   "start",
    Short: "Start the managed SuperNode",
    Long:  `Signal the running sn-manager to start SuperNode. Requires sn-manager service to be running.`,
    RunE:  runSupernodeStart,
}

func runSupernodeStart(cmd *cobra.Command, args []string) error {
    // Ensure manager is initialized
    if err := checkInitialized(); err != nil {
        return err
    }

    home := getHomeDir()

    // Check if sn-manager (service) is running via manager PID file
    managerPidPath := filepath.Join(home, managerPIDFile)
    data, err := os.ReadFile(managerPidPath)
    if err != nil {
        if os.IsNotExist(err) {
            fmt.Println("sn-manager is not running. Start it with: sn-manager start")
            return nil
        }
        return fmt.Errorf("failed to read sn-manager PID: %w", err)
    }
    pidStr := strings.TrimSpace(string(data))
    pid, _ := strconv.Atoi(pidStr)
    proc, alive := getProcessIfAlive(pid)
    if !alive {
        // Stale PID file, clean up and instruct user
        _ = os.Remove(managerPidPath)
        fmt.Println("sn-manager is not running. Start it with: sn-manager start")
        return nil
    }
    // Best-effort ping
    _ = proc.Signal(syscall.Signal(0))

    // Remove stop marker to allow the manager to start SuperNode
    stopMarker := filepath.Join(home, stopMarkerFile)
    if err := os.Remove(stopMarker); err != nil && !os.IsNotExist(err) {
        return fmt.Errorf("failed to clear stop marker: %w", err)
    }

    // If SuperNode already running, just inform
    pidPath := filepath.Join(home, supernodePIDFile)
    if p, err := readPIDFromFile(pidPath); err == nil {
        if _, ok := getProcessIfAlive(p); ok {
            fmt.Println("SuperNode is already running")
            return nil
        }
        // stale pid file
        _ = os.Remove(pidPath)
    }

    fmt.Println("Request sent. The running sn-manager will start SuperNode shortly.")
    return nil
}

