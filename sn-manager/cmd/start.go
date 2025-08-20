package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/manager"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/updater"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start SuperNode under management",
	Long: `Start the SuperNode process under sn-manager supervision.

The manager will:
- Launch the SuperNode process
- Check for updates periodically (if auto-upgrade is enabled)
- Perform automatic updates (if auto-upgrade is enabled)`,
	RunE: runStart,
}

func runStart(cmd *cobra.Command, args []string) error {
	home := getHomeDir()

	// Check if initialized
	if err := checkInitialized(); err != nil {
		return err
	}

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Handle first-time start - ensure we have a binary
	if err := ensureBinaryExists(home, cfg); err != nil {
		return fmt.Errorf("failed to ensure binary exists: %w", err)
	}

	// Check if SuperNode is initialized
	supernodeConfigPath := filepath.Join(os.Getenv("HOME"), ".supernode", "config.yml")
	if _, err := os.Stat(supernodeConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("SuperNode not initialized. Please run 'sn-manager init' first to configure your validator keys and network settings")
	}

	// Create manager instance
	mgr, err := manager.New(home)
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start SuperNode
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start supernode: %w", err)
	}

	// Start auto-updater if enabled
	var autoUpdater *updater.AutoUpdater
	if cfg.Updates.AutoUpgrade {
		autoUpdater = updater.New(home, cfg)
		autoUpdater.Start(ctx)
	}

	// Monitor SuperNode process exit
	processExitCh := make(chan error, 1)
	go func() {
		err := mgr.Wait()
		processExitCh <- err
	}()

	// Main loop - monitor for updates if auto-upgrade is enabled
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-processExitCh:
			// SuperNode process exited
			if autoUpdater != nil {
				autoUpdater.Stop()
			}
			if err != nil {
				return fmt.Errorf("supernode exited with error: %w", err)
			}
			fmt.Println("SuperNode exited")
			return nil

		case <-sigChan:
			fmt.Println("\nShutting down...")

			// Stop auto-updater if running
			if autoUpdater != nil {
				autoUpdater.Stop()
			}

			// Stop SuperNode
			if err := mgr.Stop(); err != nil {
				return fmt.Errorf("failed to stop supernode: %w", err)
			}

			return nil

		case <-ticker.C:
			// Check if binary has been updated and restart if needed
			if cfg.Updates.AutoUpgrade {
				if shouldRestart(home, mgr) {
					fmt.Println("Binary updated, restarting SuperNode...")

					// Stop current process
					if err := mgr.Stop(); err != nil {
						log.Printf("Failed to stop for restart: %v", err)
						continue
					}

					// Wait a moment
					time.Sleep(2 * time.Second)

					// Start with new binary
					if err := mgr.Start(ctx); err != nil {
						log.Printf("Failed to restart with new binary: %v", err)
						continue
					}

					fmt.Println("SuperNode restarted with new version")
				}
			}
		}
	}
}

// ensureBinaryExists ensures we have at least one SuperNode binary
func ensureBinaryExists(home string, cfg *config.Config) error {
	versionMgr := version.NewManager(home)

	// Check if we have any versions installed
	versions, err := versionMgr.ListVersions()
	if err != nil {
		return err
	}

	if len(versions) > 0 {
		// We have versions, make sure current is set
		current, err := versionMgr.GetCurrentVersion()
		if err != nil || current == "" {
			// Set the first available version as current
			if err := versionMgr.SetCurrentVersion(versions[0]); err != nil {
				return fmt.Errorf("failed to set current version: %w", err)
			}
			current = versions[0]
		}

		// Update config if current version is not set or different
		if cfg.Updates.CurrentVersion != current {
			cfg.Updates.CurrentVersion = current
			configPath := filepath.Join(home, "config.yml")
			if err := config.Save(cfg, configPath); err != nil {
				return fmt.Errorf("failed to update config with current version: %w", err)
			}
		}
		return nil
	}

	// No versions installed, download latest
	fmt.Println("No SuperNode binary found. Downloading latest version...")

	client := github.NewClient(config.GitHubRepo)
	release, err := client.GetLatestStableRelease()
	if err != nil {
		return fmt.Errorf("failed to get latest stable release: %w", err)
	}

	targetVersion := release.TagName
	fmt.Printf("Downloading SuperNode %s...\n", targetVersion)

	// Get download URL
	downloadURL, err := client.GetSupernodeDownloadURL(targetVersion)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	// Download to temp file
	tempFile := filepath.Join(home, "downloads", fmt.Sprintf("supernode-%s.tmp", targetVersion))
	os.MkdirAll(filepath.Dir(tempFile), 0755)

	// Download with progress
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
		return fmt.Errorf("failed to download binary: %w", err)
	}

	fmt.Println("Download complete. Installing...")

	// Install the version
	if err := versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
		return fmt.Errorf("failed to install version: %w", err)
	}

	// Clean up temp file
	os.Remove(tempFile)

	// Set as current version
	if err := versionMgr.SetCurrentVersion(targetVersion); err != nil {
		return fmt.Errorf("failed to set current version: %w", err)
	}

	// Update config
	cfg.Updates.CurrentVersion = targetVersion
	configPath := filepath.Join(home, "config.yml")
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Successfully installed SuperNode %s\n", targetVersion)
	return nil
}

// shouldRestart checks if the binary has been updated
func shouldRestart(home string, mgr *manager.Manager) bool {
	// Check for restart marker file
	markerPath := filepath.Join(home, ".needs_restart")
	if _, err := os.Stat(markerPath); err == nil {
		// Remove the marker and return true
		os.Remove(markerPath)
		return true
	}
	return false
}
