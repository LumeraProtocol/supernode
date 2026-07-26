package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/updater"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show SuperNode status",
	Long:  `Display the current status of the managed SuperNode process.`,
	RunE:  runStatus,
}

// printPreflightStatus prints update-blocked / rolled-back state, if any.
// Both markers live in the manager home directory and are written by the
// auto-updater's EVM preflight/rollback code path (see internal/updater/).
func printPreflightStatus(home string) {
	if data, err := os.ReadFile(updater.BlockLogPath(home)); err == nil && len(data) > 0 {
		fmt.Println("  Update Blocked: true")
		for _, line := range splitStatusLines(string(data)) {
			if line == "" {
				continue
			}
			fmt.Printf("    %s\n", line)
		}
	}
	if data, err := os.ReadFile(updater.RollbackLogPath(home)); err == nil && len(data) > 0 {
		fmt.Println("  Rolled Back: true")
		for _, line := range splitStatusLines(string(data)) {
			if line == "" {
				continue
			}
			fmt.Printf("    %s\n", line)
		}
	}
}

func splitStatusLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
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
	pidPath := filepath.Join(home, supernodePIDFile)
	pid, err := readPIDFromFile(pidPath)
	if err != nil {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Not running")
		fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
		fmt.Printf("  Manager Version: %s\n", appVersion)
		fmt.Printf("  Auto-upgrade: %v\n", cfg.Updates.AutoUpgrade)
		printPreflightStatus(home)
		return nil
	}

	// Check if process is running
	if _, alive := getProcessIfAlive(pid); !alive {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Not running (process dead)")
		fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
		fmt.Printf("  Manager Version: %s\n", appVersion)
		fmt.Printf("  Auto-upgrade: %v\n", cfg.Updates.AutoUpgrade)
		printPreflightStatus(home)
		// Clean up stale PID file
		if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove stale PID file: %v", err)
		}
		return nil
	}

	fmt.Println("SuperNode Status:")
	fmt.Printf("  Status: Running (PID %d)\n", pid)
	fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
	fmt.Printf("  Manager Version: %s\n", appVersion)
	fmt.Printf("  Auto-upgrade: %v\n", cfg.Updates.AutoUpgrade)
	printPreflightStatus(home)

	return nil
}
