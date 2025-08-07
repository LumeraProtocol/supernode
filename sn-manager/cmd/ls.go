package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List installed SuperNode versions",
	RunE:  runLs,
}

func runLs(cmd *cobra.Command, args []string) error {
	if err := checkInitialized(); err != nil {
		return err
	}

	managerHome := config.GetManagerHome()
	binariesDir := filepath.Join(managerHome, "binaries")

	entries, err := os.ReadDir(binariesDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No versions installed")
			return nil
		}
		return fmt.Errorf("failed to read binaries directory: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No versions installed")
		return nil
	}

	configPath := filepath.Join(managerHome, "config.yml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}

	sort.Strings(versions)

	fmt.Println("Installed versions:")
	for _, v := range versions {
		if v == cfg.Updates.CurrentVersion {
			fmt.Printf("→ %s (current)\n", v)
		} else {
			fmt.Printf("  %s\n", v)
		}
	}

	return nil
}