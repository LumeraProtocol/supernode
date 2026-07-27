package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	pidPath := filepath.Join(home, supernodePIDFile)
	pid, err := readPIDFromFile(pidPath)
	if err != nil {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Not running")
		fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
		fmt.Printf("  Manager Version: %s\n", appVersion)
		fmt.Printf("  Auto-upgrade: %v\n", cfg.Updates.AutoUpgrade)
		return nil
	}

	// Check if process is running
	if _, alive := getProcessIfAlive(pid); !alive {
		fmt.Println("SuperNode Status:")
		fmt.Println("  Status: Not running (process dead)")
		fmt.Printf("  Current Version: %s\n", cfg.Updates.CurrentVersion)
		fmt.Printf("  Manager Version: %s\n", appVersion)
		fmt.Printf("  Auto-upgrade: %v\n", cfg.Updates.AutoUpgrade)
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

	return nil
}
