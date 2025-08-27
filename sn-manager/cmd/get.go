package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:   "get [version]",
	Short: "Download a SuperNode version",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runGet,
}

func runGet(cmd *cobra.Command, args []string) error {
	if err := checkInitialized(); err != nil {
		return err
	}

	managerHome := config.GetManagerHome()
	versionMgr := version.NewManager(managerHome)
	client := github.NewClient(config.GitHubRepo)

	var targetVersion string
	if len(args) == 0 {
		release, err := client.GetLatestStableRelease()
		if err != nil {
			return fmt.Errorf("failed to get latest release: %w", err)
		}
		targetVersion = release.TagName
	} else {
		targetVersion = normalizeVersionTag(args[0])
	}

	fmt.Printf("Target version: %s\n", targetVersion)

	if versionMgr.IsVersionInstalled(targetVersion) {
		fmt.Printf("Already installed\n")
		return nil
	}

	downloadURL, err := client.GetSupernodeDownloadURL(targetVersion)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	tempFile := filepath.Join(managerHome, "downloads", fmt.Sprintf("supernode-%s.tmp", targetVersion))

	progress, done := newDownloadProgressPrinter()
	if err := client.DownloadBinary(downloadURL, tempFile, progress); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	done()

	if err := versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove temp file: %v", err)
	}
	fmt.Printf("✓ Installed %s\n", targetVersion)
	return nil
}
