package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/pkg/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
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
		snapshot, err := utils.ReadSupernodeUpdateSnapshot()
		if err != nil {
			return fmt.Errorf("failed to read SuperNode update configuration: %w", err)
		}
		release, err := utils.LatestReleaseForChainID(client, snapshot.ChainID)
		if err != nil {
			return fmt.Errorf("failed to get latest release for chain %q: %w", snapshot.ChainID, err)
		}
		targetVersion = release.TagName
	} else {
		targetVersion = normalizeVersionTag(args[0])
	}

	fmt.Printf("Target version: %s\n", targetVersion)

	releaseInstallLock, err := versionMgr.AcquireInstallLock()
	if err != nil {
		return fmt.Errorf("failed to lock release installation: %w", err)
	}
	defer func() {
		if releaseErr := releaseInstallLock(); releaseErr != nil {
			log.Printf("Warning: failed to release installation lock: %v", releaseErr)
		}
	}()

	if versionMgr.IsVersionInstalled(targetVersion) {
		fmt.Printf("Already installed\n")
		return nil
	}

	// Use combined tarball, then extract supernode
	tarURL, err := client.GetReleaseTarballURL(targetVersion)
	if err != nil {
		return fmt.Errorf("failed to get tarball URL: %w", err)
	}
	downloadsDir := filepath.Join(managerHome, "downloads")
	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		return fmt.Errorf("failed to create downloads dir: %w", err)
	}
	tarPath := filepath.Join(downloadsDir, fmt.Sprintf("release-%s.tar.gz", targetVersion))
	// Download tarball if not already present
	if _, statErr := os.Stat(tarPath); os.IsNotExist(statErr) {
		progress, done := newDownloadProgressPrinter()
		if err := utils.DownloadFile(tarURL, tarPath, progress); err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		done()
	}
	defer os.Remove(tarPath)

	tempFile := filepath.Join(downloadsDir, fmt.Sprintf("supernode-%s.tmp", targetVersion))
	if err := utils.ExtractFileFromTarGz(tarPath, "supernode", tempFile); err != nil {
		return fmt.Errorf("failed to extract supernode: %w", err)
	}

	if err := versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove temp file: %v", err)
	}
	fmt.Printf("✓ Installed %s\n", targetVersion)
	return nil
}
