package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var useCmd = &cobra.Command{
	Use:   "use <version>",
	Short: "Switch to a specific SuperNode version",
	Args:  cobra.ExactArgs(1),
	RunE:  runUse,
}

func runUse(cmd *cobra.Command, args []string) error {
	if err := checkInitialized(); err != nil {
		return err
	}

    targetVersion := normalizeVersionTag(args[0])

	managerHome := config.GetManagerHome()
	versionMgr := version.NewManager(managerHome)

	if !versionMgr.IsVersionInstalled(targetVersion) {
		return fmt.Errorf("version %s not installed. Run: sn-manager get %s", targetVersion, targetVersion)
	}

	if err := versionMgr.SetCurrentVersion(targetVersion); err != nil {
		return fmt.Errorf("failed to switch version: %w", err)
	}

	configPath := filepath.Join(managerHome, "config.yml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	cfg.Updates.CurrentVersion = targetVersion
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("✓ Switched to %s\n", targetVersion)
	return nil
}
