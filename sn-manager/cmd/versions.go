package cmd

import (
	"fmt"
	"os"

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
	home := getHomeDir()

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
		fmt.Println("Run 'sn-manager install' to download the latest version.")
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
