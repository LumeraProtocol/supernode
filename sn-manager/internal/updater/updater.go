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

// Package updater coordinates update checks and installs for both sn-manager
// and SuperNode. It aims to keep nodes current while minimizing disruption and
// breaking out of stuck scenarios (e.g., gateway unresponsive/busy indefinitely).
//
// Core principles:
// - Stable-only, same-major updates are eligible automatically.
// - sn-manager updates first; then SuperNode; manager restarts itself last.
// - Gateway-aware timing with bounded deferral: proceed after a hard 1h cap.
// - On unresponsive gateway and available update, proceed to avoid deadlock.
// - On unresponsive gateway with no update, request a clean restart.

type AutoUpdater struct {
	config          *config.Config
	homeDir         string
	githubClient    github.GithubClient
	versionMgr      *version.Manager
	guard           gatewayGuardIface
	ticker          *time.Ticker
	stopCh          chan struct{}
	managerVersion  string
	skipGatewayOnce bool
	busyDeferStart  time.Time
	busyDeferFor    string
}

// Use protobuf JSON decoding for gateway responses (int64s encoded as strings)

func New(homeDir string, cfg *config.Config, managerVersion string) *AutoUpdater {
	return &AutoUpdater{
		config:         cfg,
		homeDir:        homeDir,
		githubClient:   github.NewClient(config.GitHubRepo),
		versionMgr:     version.NewManager(homeDir),
		guard:          newGatewayGuard(homeDir),
		stopCh:         make(chan struct{}),
		managerVersion: managerVersion,
	}
}

func (u *AutoUpdater) Start(ctx context.Context) {
	if !u.config.Updates.AutoUpgrade {
		log.Println("Auto-upgrade is disabled")
		return
	}

	// Fixed update check interval: 10 minutes
	interval := 10 * time.Minute
	u.ticker = time.NewTicker(interval)

	// Run an immediate check on startup so restarts don't wait a full interval
	u.checkAndUpdate()

	go func() {
		for {
			select {
			case <-u.ticker.C:
				u.checkAndUpdate()
			case <-u.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// SkipGatewayCheckOnce requests the next update cycle to bypass the gateway
// idleness check (useful on first start before the gateway is up).
func (u *AutoUpdater) SkipGatewayCheckOnce() {
	u.skipGatewayOnce = true
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

// checkAndUpdate performs an update check and applies any eligible updates.
// Steps:
// 1) Fetch latest stable version from GitHub
// 2) Determine if sn-manager and/or SuperNode need updates (stable-only, same major)
// 3) Ensure update window (gateway policy + defer window)
// 4) Download release tarball (once)
// 5) Extract required binaries
// 6) Apply sn-manager update first (prepare self-restart)
// 7) Apply SuperNode update (write restart marker)
// 8) Restart sn-manager if it was updated
func (u *AutoUpdater) checkAndUpdate() {
	latest, ok := u.fetchLatestVersion()
	if !ok {
		return
	}

	managerNeeds, supernodeNeeds := u.computeUpdateNeeds(latest)
	if !managerNeeds && !supernodeNeeds {
		return
	}

	if !u.ensureUpdateWindow(managerNeeds, supernodeNeeds, latest) {
		return
	}

	tarPath, cleanup, ok := u.downloadRelease(latest)
	if !ok {
		return
	}
	defer cleanup()

	exePath, tmpManager, tmpSN, ok := u.prepareTempPaths(latest)
	if !ok {
		return
	}

	extractedManager, extractedSN := u.extractTargets(tarPath, managerNeeds, supernodeNeeds, tmpManager, tmpSN)

	managerUpdated := u.applyManagerUpdate(extractedManager, tmpManager, exePath, latest)
	u.applySupernodeUpdate(extractedSN, tmpSN, latest)

	u.maybeRestartSelf(managerUpdated)
}

// fetchLatestVersion gets the tag for the latest stable release from GitHub.
func (u *AutoUpdater) fetchLatestVersion() (string, bool) {
	release, err := u.githubClient.GetLatestStableRelease()
	if err != nil {
		log.Printf("Failed to check releases: %v", err)
		return "", false
	}
	latest := strings.TrimSpace(release.TagName)
	if latest == "" {
		return "", false
	}
	return latest, true
}

// computeUpdateNeeds decides whether manager or SuperNode should update
// under the stable-only, same-major policy.
func (u *AutoUpdater) computeUpdateNeeds(latest string) (managerNeeds, supernodeNeeds bool) {
	ver := strings.TrimSpace(u.managerVersion)
	if ver != "" && ver != "dev" && !strings.EqualFold(ver, "unknown") {
		if utils.SameMajor(ver, latest) && utils.CompareVersions(ver, latest) < 0 {
			managerNeeds = true
		}
	}
	currentSN := u.config.Updates.CurrentVersion
	supernodeNeeds = u.ShouldUpdate(currentSN, latest)
	return
}

// maxBusyDefer is a hard limit for how long we defer
// updates when the gateway reports running tasks.
// Do not make this configurable; fixed to 1 hour.
const maxBusyDefer = 1 * time.Hour

// ensureUpdateWindow applies the gateway policy and defer window.
// Behavior summary (hard 1h defer limit when busy):
// - One-time bypass per manager start (first cycle).
// - Gateway unresponsive: proceed if updates exist; else request a restart.
// - Gateway busy: defer up to maxBusyDefer (1h) for the target version; then proceed.
// - Gateway idle: proceed immediately.
func (u *AutoUpdater) ensureUpdateWindow(managerNeeds, supernodeNeeds bool, latest string) bool {
	if u.skipGatewayOnce {
		log.Println("Bypassing gateway check for initial update in this session")
		u.skipGatewayOnce = false
		// clear any prior defer state
		u.busyDeferStart = time.Time{}
		u.busyDeferFor = ""
		return true
	}
	if idle, isErr := u.guard.isIdle(); !idle {
		if isErr {
			// If gateway is not responding and an update is available, proceed.
			if managerNeeds || supernodeNeeds {
				log.Println("Gateway not responding; proceeding with update since one is available")
				u.busyDeferStart = time.Time{}
				u.busyDeferFor = ""
				return true
			}
			// No update available: request immediate restart.
			u.guard.requestRestartNow("gateway-unresponsive-no-update")
		} else {
			// Gateway is responding but busy with tasks
			if managerNeeds || supernodeNeeds {
				// Track deferral time per target version
				if u.busyDeferFor != latest {
					u.busyDeferFor = latest
					u.busyDeferStart = time.Now()
					log.Printf("Gateway busy; deferring update to %s (max %s)", latest, maxBusyDefer)
					return false
				}
				// Same version still pending; check if defer window exceeded
				elapsed := time.Since(u.busyDeferStart)
				if elapsed >= maxBusyDefer {
					log.Printf("Gateway busy for %s; exceeding max defer window, proceeding with update to %s", elapsed.Round(time.Second), latest)
					u.busyDeferStart = time.Time{}
					u.busyDeferFor = ""
					return true
				}
				// Continue deferring until max window reached
				remaining := (maxBusyDefer - elapsed).Round(time.Second)
				log.Printf("Gateway busy; deferring update to %s. Remaining defer window: %s", latest, remaining)
				return false
			}
			log.Println("Gateway busy, no updates available; nothing to do")
		}
		return false
	}
	// Idle: clear any prior defer state
	if !u.busyDeferStart.IsZero() {
		u.busyDeferStart = time.Time{}
		u.busyDeferFor = ""
	}
	return true
}

// downloadRelease downloads the single release tarball for the given version.
// The returned cleanup removes the tarball after use.
func (u *AutoUpdater) downloadRelease(latest string) (string, func(), bool) {
	tarURL, err := u.githubClient.GetReleaseTarballURL(latest)
	if err != nil {
		log.Printf("Failed to get tarball URL: %v", err)
		return "", func() {}, false
	}
	downloadsDir := filepath.Join(u.homeDir, "downloads")
	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		log.Printf("Failed to create downloads directory: %v", err)
		return "", func() {}, false
	}
	tarPath := filepath.Join(downloadsDir, fmt.Sprintf("release-%s.tar.gz", latest))
	if err := utils.DownloadFile(tarURL, tarPath, nil); err != nil {
		log.Printf("Failed to download tarball: %v", err)
		return "", func() {}, false
	}
	cleanup := func() {
		if err := os.Remove(tarPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove tarball: %v", err)
		}
	}
	return tarPath, cleanup, true
}

// prepareTempPaths computes the absolute path to the running sn-manager
// executable and temp paths where extracted binaries are staged.
func (u *AutoUpdater) prepareTempPaths(latest string) (exePath, tmpManager, tmpSN string, ok bool) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("Cannot determine executable path: %v", err)
		return "", "", "", false
	}
	exe, _ = filepath.EvalSymlinks(exe)
	tmpManager = exe + ".new"
	tmpSN = filepath.Join(u.homeDir, "downloads", fmt.Sprintf("supernode-%s.tmp", latest))
	return exe, tmpManager, tmpSN, true
}

// extractTargets extracts requested binaries from the tarball to the given
// temp paths. Returns which targets were found and extracted.
func (u *AutoUpdater) extractTargets(tarPath string, managerNeeds, supernodeNeeds bool, tmpManager, tmpSN string) (extractedManager, extractedSN bool) {
	targets := map[string]string{}
	if managerNeeds {
		targets["sn-manager"] = tmpManager
	}
	if supernodeNeeds {
		targets["supernode"] = tmpSN
	}
	found, err := utils.ExtractMultipleFromTarGz(tarPath, targets)
	if err != nil {
		log.Printf("Extraction error: %v", err)
		return false, false
	}
	return managerNeeds && found["sn-manager"], supernodeNeeds && found["supernode"]
}

// applyManagerUpdate replaces the current sn-manager binary if it was
// extracted from the tarball. Returns true if an update was applied.
func (u *AutoUpdater) applyManagerUpdate(extracted bool, tmpManager, exePath, latest string) bool {
	if !extracted {
		return false
	}
	if err := os.Rename(tmpManager, exePath); err != nil {
		_ = os.Remove(tmpManager)
		log.Printf("Cannot replace sn-manager (%s). Update manually: %v", exePath, err)
		return false
	}
	if dirF, err := os.Open(filepath.Dir(exePath)); err == nil {
		_ = dirF.Sync()
		dirF.Close()
	}
	log.Printf("sn-manager updated to %s", latest)
	return true
}

// applySupernodeUpdate installs and activates the new SuperNode binary if
// extracted, updates config and writes a restart marker for the manager loop.
func (u *AutoUpdater) applySupernodeUpdate(extracted bool, tmpSN, latest string) {
	if !extracted {
		return
	}
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
}

// maybeRestartSelf restarts the process when the manager was updated.
func (u *AutoUpdater) maybeRestartSelf(managerUpdated bool) {
	if !managerUpdated {
		return
	}
	log.Printf("Self-update applied, restarting service...")
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(3)
	}()
}
