package updater

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
)

// Global updater timing constants
const (
	// updateCheckInterval is how often the periodic updater runs
	updateCheckInterval = 10 * time.Minute
	// forceUpdateAfter is the age threshold after a release is published
	// beyond which updates are applied regardless of normal gates (policy only)
	forceUpdateAfter = 30 * time.Minute
)

type AutoUpdater struct {
	config         *config.Config
	homeDir        string
	githubClient   github.GithubClient
	versionMgr     *version.Manager
	ticker         *time.Ticker
	stopCh         chan struct{}
	managerVersion string
}

// Use protobuf JSON decoding for gateway responses (int64s encoded as strings)

func New(homeDir string, cfg *config.Config, managerVersion string) *AutoUpdater {
	return &AutoUpdater{
		config:         cfg,
		homeDir:        homeDir,
		githubClient:   github.NewClient(config.GitHubRepo),
		versionMgr:     version.NewManager(homeDir),
		stopCh:         make(chan struct{}),
		managerVersion: managerVersion,
	}
}

func (u *AutoUpdater) Start(ctx context.Context) {
	if !u.config.Updates.AutoUpgrade {
		log.Println("Auto-upgrade is disabled")
		return
	}

	// Fixed update check interval
	u.ticker = time.NewTicker(updateCheckInterval)

	// Run an immediate check on startup so restarts don't wait a full interval
	u.checkAndUpdateCombined(false)

	go func() {
		for {
			select {
			case <-u.ticker.C:
				u.checkAndUpdateCombined(false)
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
		u.ticker = nil
	}
	// Make Stop idempotent by only closing once
	select {
	case <-u.stopCh:
		// already closed
	default:
		close(u.stopCh)
	}
}

func (u *AutoUpdater) ShouldUpdate(current, latest string) bool {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	// Skip pre-release targets (beta, alpha, rc, etc.)
	if strings.Contains(latest, "-") {
		return false
	}

	// Handle pre-release versions (e.g., "1.7.1-beta" -> "1.7.1")
	currentBase := strings.Split(current, "-")[0]
	latestBase := strings.Split(latest, "-")[0]

	// Only update within same major version (allow minor and patch updates)
	if !utils.SameMajor(currentBase, latestBase) {
		// Quietly skip major jumps; manual upgrade path covers this
		return false
	}

	// Compare base versions (stable releases only)
	cmp := utils.CompareVersions(currentBase, latestBase)
	if cmp == 0 && current != currentBase {
		// Current is a prerelease for the same base; update to stable
		return true
	}
	if cmp < 0 {
		return true
	}
	return false
}

// isGatewayIdle returns (idle, isError). When isError is true,
// the gateway could not be reliably checked (network/error/invalid).
// When isError is false and idle is false, the gateway is busy.

// checkAndUpdateCombined performs a single release check and, if needed,
// downloads the release tarball once to update sn-manager and SuperNode.
// Order: update sn-manager first (prepare new binary), then SuperNode, then
// trigger restart if manager was updated.
// ForceSyncToLatest performs a one-shot forced sync to the latest stable
// release, bypassing standard gating checks (gateway idle, same-major policy).
// Intended for mandatory checks at manager start.
func (u *AutoUpdater) ForceSyncToLatest(_ context.Context) {
	u.checkAndUpdateCombined(true)
}

// checkAndUpdateCombined performs a single release check and, if needed,
// downloads the release tarball once to update sn-manager and SuperNode.
// If force is true, bypass gateway idleness and version policy checks.
func (u *AutoUpdater) checkAndUpdateCombined(force bool) {

	// Fetch latest stable release once
	release, err := u.githubClient.GetLatestStableRelease()
	if err != nil {
		log.Printf("Failed to check releases: %v", err)
		return
	}

	latest := strings.TrimSpace(release.TagName)
	if latest == "" {
		return
	}

	// If the latest release has been out for > 4 hours, elevate to force mode
	if !force {
		if !release.PublishedAt.IsZero() && time.Since(release.PublishedAt) > forceUpdateAfter {
			force = true
		}
	}

	// Determine if sn-manager should update (same criteria: stable, same major)
	managerNeedsUpdate := false
	ver := strings.TrimSpace(u.managerVersion)
	if ver != "" && ver != "dev" && !strings.EqualFold(ver, "unknown") {
		if force {
			managerNeedsUpdate = !strings.EqualFold(ver, latest)
		} else {
			if utils.SameMajor(ver, latest) && utils.CompareVersions(ver, latest) < 0 {
				managerNeedsUpdate = true
			}
		}
	}

	// Determine if SuperNode should update using existing policy
	currentSN := u.config.Updates.CurrentVersion
	supernodeNeedsUpdate := false
	if force {
		supernodeNeedsUpdate = !strings.EqualFold(strings.TrimPrefix(currentSN, "v"), strings.TrimPrefix(latest, "v"))
	} else {
		supernodeNeedsUpdate = u.ShouldUpdate(currentSN, latest)
	}

	if !managerNeedsUpdate && !supernodeNeedsUpdate {
		return
	}

	// Download the combined release tarball once
	tarURL, err := u.githubClient.GetReleaseTarballURL(latest)
	if err != nil {
		log.Printf("Failed to get tarball URL: %v", err)
		return
	}
	// Ensure downloads directory exists
	downloadsDir := filepath.Join(u.homeDir, "downloads")
	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		log.Printf("Failed to create downloads directory: %v", err)
		return
	}

	tarPath := filepath.Join(downloadsDir, fmt.Sprintf("release-%s.tar.gz", latest))
	if err := utils.DownloadFile(tarURL, tarPath, nil); err != nil {
		log.Printf("Failed to download tarball: %v", err)
		return
	}
	defer func() {
		if err := os.Remove(tarPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove tarball: %v", err)
		}
	}()

	// Prepare paths for extraction targets
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("Cannot determine executable path: %v", err)
		return
	}
	exePath, _ = filepath.EvalSymlinks(exePath)
	tmpManager := exePath + ".new"
	tmpSN := filepath.Join(u.homeDir, "downloads", fmt.Sprintf("supernode-%s.tmp", latest))

	// Build extraction targets by base name
	targets := map[string]string{}
	if managerNeedsUpdate {
		targets["sn-manager"] = tmpManager
	}
	if supernodeNeedsUpdate {
		targets["supernode"] = tmpSN
	}

	found, err := utils.ExtractMultipleFromTarGz(tarPath, targets)
	if err != nil {
		log.Printf("Extraction error: %v", err)
		return
	}

	extractedManager := managerNeedsUpdate && found["sn-manager"]
	extractedSN := supernodeNeedsUpdate && found["supernode"]

	// Apply sn-manager update first
	managerUpdated := false
	if managerNeedsUpdate {
		if extractedManager {
			if err := os.Rename(tmpManager, exePath); err != nil {
				os.Remove(tmpManager)
				log.Printf("Cannot replace sn-manager (%s). Update manually: %v", exePath, err)
			} else {
				if dirF, err := os.Open(filepath.Dir(exePath)); err == nil {
					_ = dirF.Sync()
					dirF.Close()
				}
				managerUpdated = true
				log.Printf("sn-manager updated to %s", latest)
			}
		} else {
			log.Printf("sn-manager binary not found in tarball; skipping")
		}
	}

	// Apply SuperNode update (idle already verified) and extracted
	if supernodeNeedsUpdate {
		if extractedSN {
			if err := u.versionMgr.InstallVersion(latest, tmpSN); err != nil {
				log.Printf("Failed to install SuperNode: %v", err)
			} else {
				if err := u.versionMgr.SetCurrentVersion(latest); err != nil {
					log.Printf("Failed to activate SuperNode %s: %v", latest, err)
				} else {
					u.config.Updates.CurrentVersion = latest
					if err := config.Save(u.config, filepath.Join(u.homeDir, "config.yml")); err != nil {
						log.Printf("Failed to save config: %v", err)
					}
					if err := os.WriteFile(filepath.Join(u.homeDir, ".needs_restart"), []byte(latest), 0644); err != nil {
						log.Printf("Failed to write restart marker: %v", err)
					}
					log.Printf("SuperNode updated to %s", latest)
				}
			}
			if err := os.Remove(tmpSN); err != nil && !os.IsNotExist(err) {
				log.Printf("Warning: failed to remove temp supernode: %v", err)
			}
		} else {
			log.Printf("supernode binary not found in tarball; skipping")
		}
	}

	// If manager updated, restart service after completing all work
	if managerUpdated {
		log.Printf("Self-update applied, restarting service...")
		go func() {
			time.Sleep(500 * time.Millisecond)
			os.Exit(3)
		}()
	}
}

// handleGatewayError increments an error counter in a rolling 5-minute window
// and when the threshold is reached, requests a clean SuperNode restart by
// writing the standard restart marker consumed by the manager monitor.
