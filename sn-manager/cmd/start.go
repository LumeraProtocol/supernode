package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/manager"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/updater"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start SuperNode under management",
	Long: `Start the SuperNode process under sn-manager supervision.

The manager will:
- Launch the SuperNode process
- Monitor the process and restart on crashes
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

	// Check if sn-manager is already running
	managerPidPath := filepath.Join(home, managerPIDFile)
	if pidData, err := os.ReadFile(managerPidPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(pidData))); err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				if err := process.Signal(syscall.Signal(0)); err == nil {
					// Manager is already running
					return fmt.Errorf("sn-manager is already running (PID %d)", pid)
				}
			}
		}
		// Stale PID file, remove it
		if err := os.Remove(managerPidPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove stale manager PID file: %v", err)
		}
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
	if err := ensureSupernodeInitialized(); err != nil {
		return err
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

	// Save sn-manager PID early to minimize race for multiple instances
	managerPidPath = filepath.Join(home, managerPIDFile)
	if err := os.WriteFile(managerPidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		log.Printf("Warning: failed to save sn-manager PID file: %v", err)
	}
	defer os.Remove(managerPidPath)

	// If there was a previous explicit stop, clear it now since user called start
	stopMarkerPath := filepath.Join(home, stopMarkerFile)
	if _, err := os.Stat(stopMarkerPath); err == nil {
		if err := os.Remove(stopMarkerPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove stop marker: %v", err)
		}
	}

	// Sanity check: if auto-upgrade is enabled and sn-manager binary dir is not writable, error and exit with guidance
	if exePath, err := os.Executable(); err == nil {
		if exeReal, err := filepath.EvalSymlinks(exePath); err == nil {
			exeDir := filepath.Dir(exeReal)
			if ok, _ := utils.IsDirWritable(exeDir); !ok {
				if cfg.Updates.AutoUpgrade {
					return fmt.Errorf(
						"auto-upgrade is enabled but sn-manager binary directory is not writable (%s).\nInstall sn-manager to a user-writable path and update your systemd unit as per the README: %s\nRecommended path: %s",
						exeDir,
						"https://github.com/LumeraProtocol/supernode/blob/master/sn-manager/README.md#fix-non-writable-install",
						filepath.Join(home, "bin", "sn-manager"),
					)
				}
				// If auto-upgrade is disabled, warn but continue
				log.Printf("Warning: sn-manager binary directory is not writable (%s). Self-update is disabled.", exeDir)
			}
		}
	}

	// Start auto-updater if enabled
	var autoUpdater *updater.AutoUpdater
	if cfg.Updates.AutoUpgrade {
		autoUpdater = updater.New(home, cfg, appVersion)
		autoUpdater.Start(ctx)
	}

	// Start monitoring in a goroutine
	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- mgr.Monitor(ctx)
	}()

	// Wait for shutdown signal or monitor exit
	select {
	case <-sigChan:
		fmt.Println("\nShutting down...")

		// Stop auto-updater if running
		if autoUpdater != nil {
			autoUpdater.Stop()
		}

		// Cancel context to stop monitoring
		cancel()

		// Wait for monitor to finish
		<-monitorDone

		// Stop SuperNode if still running
		if mgr.IsRunning() {
			if err := mgr.Stop(); err != nil {
				log.Printf("Failed to stop supernode: %v", err)
			}
		}

		return nil

	case err := <-monitorDone:
		// Monitor exited unexpectedly
		if err != nil {
			return fmt.Errorf("monitor error: %w", err)
		}
		return nil
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

	// No versions installed, download latest tarball and extract supernode
	fmt.Println("No SuperNode binary found. Downloading latest version...")

	client := github.NewClient(config.GitHubRepo)
	release, err := client.GetLatestStableRelease()
	if err != nil {
		return fmt.Errorf("failed to get latest stable release: %w", err)
	}

	targetVersion := release.TagName
	fmt.Printf("Downloading SuperNode %s...\n", targetVersion)

	// Download tarball
	tarURL, err := client.GetReleaseTarballURL(targetVersion)
	if err != nil {
		return fmt.Errorf("failed to get tarball URL: %w", err)
	}
	downloadsDir := filepath.Join(home, "downloads")
	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		return fmt.Errorf("failed to create downloads dir: %w", err)
	}
	tarPath := filepath.Join(downloadsDir, fmt.Sprintf("release-%s.tar.gz", targetVersion))
	// Download tarball if not already present
	if _, statErr := os.Stat(tarPath); os.IsNotExist(statErr) {
		progress, done := newDownloadProgressPrinter()
		if err := utils.DownloadFile(tarURL, tarPath, progress); err != nil {
			return fmt.Errorf("failed to download tarball: %w", err)
		}
		done()
	}
	defer os.Remove(tarPath)

	// Extract supernode to temp
	tempFile := filepath.Join(downloadsDir, fmt.Sprintf("supernode-%s.tmp", targetVersion))
	if err := utils.ExtractFileFromTarGz(tarPath, "supernode", tempFile); err != nil {
		return fmt.Errorf("failed to extract supernode: %w", err)
	}

	fmt.Println("Download complete. Installing...")

	// Install the version
	if err := versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
		return fmt.Errorf("failed to install version: %w", err)
	}

	// Clean up temp file
	if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove temp file: %v", err)
	}

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
