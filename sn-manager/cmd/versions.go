package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var versionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "List installed SuperNode versions",
	Long:  `Display all downloaded SuperNode versions and indicate which is currently active.`,
	RunE:  runVersions,
}

func runVersions(cmd *cobra.Command, args []string) error {
	// Determine home directory
	home := homeDir
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		home = filepath.Join(userHome, ".sn-manager")
	}

	// Create version manager
	versionMgr := version.NewManager(home)

	// Get current version
	current, _ := versionMgr.GetCurrentVersion()

	// List all versions
	versions, err := versionMgr.ListVersions()
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	if len(versions) == 0 {
		fmt.Println("No SuperNode versions installed.")
		fmt.Println("Run 'sn-manager upgrade' to download the latest version.")
		return nil
	}

	fmt.Println("Installed SuperNode versions:")
	for _, v := range versions {
		if v == current {
			fmt.Printf("  * %s (current)\n", v)
		} else {
			fmt.Printf("    %s\n", v)
		}
		
		// Show binary info
		binaryPath := versionMgr.GetVersionBinary(v)
		if info, err := os.Stat(binaryPath); err == nil {
			fmt.Printf("      Size: %.2f MB\n", float64(info.Size())/(1024*1024))
			fmt.Printf("      Modified: %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
		}
	}

	return nil
}