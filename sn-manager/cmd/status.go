package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show SuperNode status",
	Long:  `Display the current status of the managed SuperNode process.`,
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	home := getHomeDir()

	// Check if initialized
	if err := checkInitialized(); err != nil {
		fmt.Println("SuperNode Status: Not initialized")
		return nil
	}

	// Load config to get version info
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Check PID file
	pidPath := filepath.Join(home, "supernode.pid")
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Not running")
		fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
		fmt.Printf("  Manager Version: %s\n", appVersion)
		return nil
	}

	// Parse PID
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Invalid PID file")
		return nil
	}

	// Check if process is running
	process, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Not running (stale PID)")
		fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
		return nil
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Not running (process dead)")
		fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
		// Clean up stale PID file
		os.Remove(pidPath)
		return nil
	}

	fmt.Println("SuperNode Status:")
	fmt.Printf("  Status: Running (PID %d)\n", pid)
	fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
	fmt.Printf("  Manager Version: %s\n", appVersion)
	fmt.Printf("  Auto-upgrade: %v\n", cfg.Updates.AutoUpgrade)

	return nil
}
