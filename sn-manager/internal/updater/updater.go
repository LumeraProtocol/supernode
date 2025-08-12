package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/utils"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/version"
)

type AutoUpdater struct {
	config       *config.Config
	homeDir      string
	githubClient github.GithubClient
	versionMgr   *version.Manager
	gatewayURL   string
	ticker       *time.Ticker
	stopCh       chan struct{}
}

type StatusResponse struct {
	RunningTasks []struct {
		ServiceName string   `json:"service_name"`
		TaskIDs     []string `json:"task_ids"`
		TaskCount   int      `json:"task_count"`
	} `json:"running_tasks"`
}

func New(homeDir string, cfg *config.Config) *AutoUpdater {
	return &AutoUpdater{
		config:       cfg,
		homeDir:      homeDir,
		githubClient: github.NewClient(config.GitHubRepo),
		versionMgr:   version.NewManager(homeDir),
		gatewayURL:   "http://localhost:8002/api/supernode/status",
		stopCh:       make(chan struct{}),
	}
}

func (u *AutoUpdater) Start(ctx context.Context) {
	if !u.config.Updates.AutoUpgrade {
		log.Println("Auto-upgrade is disabled")
		return
	}

	interval := time.Duration(u.config.Updates.CheckInterval) * time.Second
	u.ticker = time.NewTicker(interval)

	log.Printf("Starting auto-updater (checking every %v)", interval)

	u.checkAndUpdate(ctx)

	go func() {
		for {
			select {
			case <-u.ticker.C:
				u.checkAndUpdate(ctx)
			case <-u.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (u *AutoUpdater) Stop() {
	if u.ticker != nil {
		u.ticker.Stop()
	}
	close(u.stopCh)
}

func (u *AutoUpdater) checkAndUpdate(ctx context.Context) {
	log.Println("Checking for updates...")

	release, err := u.githubClient.GetLatestRelease()
	if err != nil {
		log.Printf("Failed to check for updates: %v", err)
		return
	}

	latestVersion := release.TagName
	currentVersion := u.config.Updates.CurrentVersion

	if !u.shouldUpdate(currentVersion, latestVersion) {
		log.Printf("Current version %s is up to date", currentVersion)
		return
	}

	log.Printf("New version available: %s -> %s", currentVersion, latestVersion)

	if !u.isGatewayIdle() {
		log.Println("Gateway has running tasks, skipping update")
		return
	}

	if err := u.performUpdate(latestVersion); err != nil {
		log.Printf("Failed to perform update: %v", err)
		return
	}

	log.Printf("Successfully updated to version %s", latestVersion)
}

func (u *AutoUpdater) shouldUpdate(current, latest string) bool {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	currentParts := strings.Split(current, ".")
	latestParts := strings.Split(latest, ".")

	if len(currentParts) < 3 || len(latestParts) < 3 {
		return false
	}

	if currentParts[0] != latestParts[0] || currentParts[1] != latestParts[1] {
		return false
	}

	if currentParts[2] != latestParts[2] {
		cmp := utils.CompareVersions(current, latest)
		if cmp < 0 {
			log.Printf("Update available (%s -> %s)", current, latest)
			return true
		}
	}

	return false
}

func (u *AutoUpdater) isGatewayIdle() bool {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(u.gatewayURL)
	if err != nil {
		log.Printf("Failed to check gateway status: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Gateway returned status %d", resp.StatusCode)
		return false
	}

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		log.Printf("Failed to decode gateway response: %v", err)
		return false
	}

	totalTasks := 0
	for _, service := range status.RunningTasks {
		totalTasks += service.TaskCount
	}

	if totalTasks > 0 {
		log.Printf("Gateway has %d running tasks", totalTasks)
		return false
	}

	return true
}

func (u *AutoUpdater) performUpdate(targetVersion string) error {
	log.Printf("Downloading version %s...", targetVersion)

	downloadURL, err := u.githubClient.GetSupernodeDownloadURL(targetVersion)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	tempFile := filepath.Join(u.homeDir, "downloads", fmt.Sprintf("supernode-%s.tmp", targetVersion))

	progress := func(downloaded, total int64) {
		if total > 0 {
			percent := int(downloaded * 100 / total)
			if percent%20 == 0 {
				log.Printf("Download progress: %d%%", percent)
			}
		}
	}

	if err := u.githubClient.DownloadBinary(downloadURL, tempFile, progress); err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}

	if err := u.versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
		return fmt.Errorf("failed to install version: %w", err)
	}

	os.Remove(tempFile)

	if err := u.versionMgr.SetCurrentVersion(targetVersion); err != nil {
		return fmt.Errorf("failed to set current version: %w", err)
	}

	u.config.Updates.CurrentVersion = targetVersion
	configPath := filepath.Join(u.homeDir, "config.yml")
	if err := config.Save(u.config, configPath); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	markerPath := filepath.Join(u.homeDir, ".needs_restart")
	if err := os.WriteFile(markerPath, []byte(targetVersion), 0644); err != nil {
		log.Printf("Failed to create restart marker: %v", err)
	}

	return nil
}
