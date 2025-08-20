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

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/LumeraProtocol/supernode/v2/supernode/node/supernode/gateway"
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
	// Use the correct gateway endpoint with imported constants
	gatewayURL := fmt.Sprintf("http://localhost:%d/api/v1/status", gateway.DefaultGatewayPort)
	
	return &AutoUpdater{
		config:       cfg,
		homeDir:      homeDir,
		githubClient: github.NewClient(config.GitHubRepo),
		versionMgr:   version.NewManager(homeDir),
		gatewayURL:   gatewayURL,
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

	log.Printf("Version comparison: current=%s, latest=%s", currentVersion, latestVersion)

	if !u.ShouldUpdate(currentVersion, latestVersion) {
		log.Printf("Current version %s is up to date", currentVersion)
		return
	}

	log.Printf("Update available: %s -> %s", currentVersion, latestVersion)

	if !u.isGatewayIdle() {
		log.Println("Gateway busy, skipping update")
		return
	}

	if err := u.performUpdate(latestVersion); err != nil {
		log.Printf("Update failed: %v", err)
		return
	}

	log.Printf("Updated to %s", latestVersion)
}

func (u *AutoUpdater) ShouldUpdate(current, latest string) bool {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	// Skip pre-release versions (beta, alpha, rc, etc.)
	if strings.Contains(latest, "-") {
		log.Printf("Skipping pre-release version: %s", latest)
		return false
	}

	// Handle pre-release versions (e.g., "1.7.1-beta" -> "1.7.1")
	currentBase := strings.Split(current, "-")[0]
	latestBase := strings.Split(latest, "-")[0]

	log.Printf("Comparing base versions: current=%s, latest=%s", currentBase, latestBase)

	currentParts := strings.Split(currentBase, ".")
	latestParts := strings.Split(latestBase, ".")

	if len(currentParts) < 3 || len(latestParts) < 3 {
		log.Printf("Invalid version format: current=%s, latest=%s", currentBase, latestBase)
		return false
	}

	// Only update within same major version (allow minor and patch updates)
	if currentParts[0] != latestParts[0] {
		log.Printf("Major version mismatch, skipping update: %s vs %s", 
			currentParts[0], latestParts[0])
		return false
	}

	// Compare base versions (stable releases only)
	cmp := utils.CompareVersions(currentBase, latestBase)
	if cmp < 0 {
		log.Printf("Update needed: %s < %s", currentBase, latestBase)
		return true
	}

	log.Printf("No update needed: %s >= %s", currentBase, latestBase)
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
		log.Printf("Gateway busy: %d running tasks", totalTasks)
		return false
	}

	return true
}

func (u *AutoUpdater) performUpdate(targetVersion string) error {
	downloadURL, err := u.githubClient.GetSupernodeDownloadURL(targetVersion)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	tempFile := filepath.Join(u.homeDir, "downloads", fmt.Sprintf("supernode-%s.tmp", targetVersion))

	// Silent download - no progress reporting
	if err := u.githubClient.DownloadBinary(downloadURL, tempFile, nil); err != nil {
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
