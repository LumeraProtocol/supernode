package cmd

import (
	"fmt"
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
		release, err := client.GetLatestRelease()
		if err != nil {
			return fmt.Errorf("failed to get latest release: %w", err)
		}
		targetVersion = release.TagName
	} else {
		targetVersion = args[0]
		if targetVersion[0] != 'v' {
			targetVersion = "v" + targetVersion
		}
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
	
	var lastPercent int
	progress := func(downloaded, total int64) {
		if total > 0 {
			percent := int(downloaded * 100 / total)
			if percent != lastPercent && percent%10 == 0 {
				fmt.Printf("\rProgress: %d%%", percent)
				lastPercent = percent
			}
		}
	}

	if err := client.DownloadBinary(downloadURL, tempFile, progress); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	fmt.Println()

	if err := versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	os.Remove(tempFile)
	fmt.Printf("✓ Installed %s\n", targetVersion)
	return nil
}