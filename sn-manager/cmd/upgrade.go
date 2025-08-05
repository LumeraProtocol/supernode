package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var (
	forceUpgrade bool
	skipDownload bool
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade [version]",
	Short: "Upgrade SuperNode to a new version",
	Long: `Upgrade SuperNode to the latest version or a specific version.

Examples:
  sn-manager upgrade              # Upgrade to latest
  sn-manager upgrade v1.8.0        # Upgrade to specific version`,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&forceUpgrade, "force", false, "Force upgrade even if already running this version")
	upgradeCmd.Flags().BoolVar(&skipDownload, "skip-download", false, "Skip download if version already exists")
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	// Determine home directory
	home := homeDir
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		home = filepath.Join(userHome, ".sn-manager")
	}

	// Load config
	configPath := filepath.Join(home, "config.yml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create GitHub client
	client := github.NewClient(cfg.Updates.GitHubRepo)
	
	// Determine target version
	var targetVersion string
	if len(args) > 0 {
		targetVersion = args[0]
		fmt.Printf("Upgrading to version %s...\n", targetVersion)
	} else {
		// Get latest release
		release, err := client.GetLatestRelease()
		if err != nil {
			return fmt.Errorf("failed to get latest release: %w", err)
		}
		targetVersion = release.TagName
		fmt.Printf("Upgrading to latest version %s...\n", targetVersion)
	}

	// Check if already running this version
	if cfg.Updates.CurrentVersion == targetVersion && !forceUpgrade {
		fmt.Printf("Already running version %s. Use --force to reinstall.\n", targetVersion)
		return nil
	}

	// Create version manager
	versionMgr := version.NewManager(home)

	// Check if version is already downloaded
	if versionMgr.IsVersionInstalled(targetVersion) && skipDownload {
		fmt.Printf("Version %s is already installed. Switching...\n", targetVersion)
	} else {
		// Download the binary
		fmt.Printf("Downloading SuperNode %s...\n", targetVersion)
		
		downloadURL, err := client.GetSupernodeDownloadURL(targetVersion)
		if err != nil {
			return fmt.Errorf("failed to get download URL: %w", err)
		}

		// Download to temp file
		tempFile := filepath.Join(home, "downloads", fmt.Sprintf("supernode-%s.tmp", targetVersion))
		
		// Progress callback
		var lastPercent int
		progress := func(downloaded, total int64) {
			if total > 0 {
				percent := int(downloaded * 100 / total)
				if percent != lastPercent && percent%10 == 0 {
					fmt.Printf("Progress: %d%%\n", percent)
					lastPercent = percent
				}
			}
		}

		if err := client.DownloadBinary(downloadURL, tempFile, progress); err != nil {
			return fmt.Errorf("failed to download binary: %w", err)
		}

		fmt.Println("Download complete. Installing...")

		// Install the version
		if err := versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
			return fmt.Errorf("failed to install version: %w", err)
		}

		// Clean up temp file
		os.Remove(tempFile)
	}

	// Check if SuperNode is currently running
	pidPath := filepath.Join(home, "supernode.pid")
	needsRestart := false
	
	if pidData, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(string(pidData)); err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				if err := process.Signal(syscall.Signal(0)); err == nil {
					fmt.Println("SuperNode is currently running. Stopping for upgrade...")
					needsRestart = true
					
					// Send SIGTERM for graceful shutdown
					if err := process.Signal(syscall.SIGTERM); err != nil {
						return fmt.Errorf("failed to stop SuperNode: %w", err)
					}
					
					// Wait a moment for shutdown
					// TODO: Implement proper wait with timeout
					fmt.Println("Waiting for SuperNode to stop...")
				}
			}
		}
	}

	// Update symlink to new version
	fmt.Printf("Switching to version %s...\n", targetVersion)
	if err := versionMgr.SetCurrentVersion(targetVersion); err != nil {
		return fmt.Errorf("failed to set current version: %w", err)
	}

	// Update config with new version
	cfg.Updates.CurrentVersion = targetVersion
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	// Clean up old versions
	if cfg.Updates.KeepVersions > 0 {
		if err := versionMgr.CleanupOldVersions(cfg.Updates.KeepVersions); err != nil {
			fmt.Printf("Warning: failed to cleanup old versions: %v\n", err)
		}
	}

	fmt.Printf("\n✓ Successfully upgraded to version %s\n", targetVersion)
	
	if needsRestart {
		fmt.Println("\nSuperNode was stopped for upgrade.")
		fmt.Println("Run 'sn-manager start' to restart with the new version.")
	} else {
		fmt.Println("\nRun 'sn-manager start' to start SuperNode with the new version.")
	}

	return nil
}