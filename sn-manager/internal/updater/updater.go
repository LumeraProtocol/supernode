package updater

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/LumeraProtocol/supernode/v2/supernode/node/supernode/gateway"
	"google.golang.org/protobuf/encoding/protojson"
)

const gatewayTimeout = 5 * time.Second

type AutoUpdater struct {
	config         *config.Config
	homeDir        string
	githubClient   github.GithubClient
	versionMgr     *version.Manager
	gatewayURL     string
	ticker         *time.Ticker
	stopCh         chan struct{}
	managerVersion string
	// Gateway error backoff state
	gwErrCount       int
	gwErrWindowStart time.Time
}

// Use protobuf JSON decoding for gateway responses (int64s encoded as strings)

func New(homeDir string, cfg *config.Config, managerVersion string) *AutoUpdater {
	// Use the correct gateway endpoint with imported constants
	gatewayURL := fmt.Sprintf("http://localhost:%d/api/v1/status", gateway.DefaultGatewayPort)

	return &AutoUpdater{
		config:         cfg,
		homeDir:        homeDir,
		githubClient:   github.NewClient(config.GitHubRepo),
		versionMgr:     version.NewManager(homeDir),
		gatewayURL:     gatewayURL,
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
	u.checkAndUpdateCombined()

	go func() {
		for {
			select {
			case <-u.ticker.C:
				u.checkAndUpdateCombined()
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
func (u *AutoUpdater) isGatewayIdle() (bool, bool) {
	client := &http.Client{Timeout: gatewayTimeout}

	resp, err := client.Get(u.gatewayURL)
	if err != nil {
		log.Printf("Failed to check gateway status: %v", err)
		// Error contacting gateway
		return false, true
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Gateway returned status %d, not safe to update", resp.StatusCode)
		return false, true
	}

	var status pb.StatusResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read gateway response: %v", err)
		return false, true
	}
	if err := protojson.Unmarshal(body, &status); err != nil {
		log.Printf("Failed to decode gateway response: %v", err)
		return false, true
	}

	totalTasks := 0
	for _, service := range status.RunningTasks {
		totalTasks += int(service.TaskCount)
	}

	if totalTasks > 0 {
		log.Printf("Gateway busy: %d running tasks", totalTasks)
		return false, false
	}

	return true, false
}

// checkAndUpdateCombined performs a single release check and, if needed,
// downloads the release tarball once to update sn-manager and SuperNode.
// Order: update sn-manager first (prepare new binary), then SuperNode, then
// trigger restart if manager was updated.
func (u *AutoUpdater) checkAndUpdateCombined() {

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

	// Determine if sn-manager should update (same criteria: stable, same major)
	managerNeedsUpdate := false
	ver := strings.TrimSpace(u.managerVersion)
	if ver != "" && ver != "dev" && !strings.EqualFold(ver, "unknown") {
		if utils.SameMajor(ver, latest) && utils.CompareVersions(ver, latest) < 0 {
			managerNeedsUpdate = true
		}
	}

	// Determine if SuperNode should update using existing policy
	currentSN := u.config.Updates.CurrentVersion
	supernodeNeedsUpdate := u.ShouldUpdate(currentSN, latest)

	if !managerNeedsUpdate && !supernodeNeedsUpdate {
		return
	}

	// Gate all updates (manager + SuperNode) on gateway idleness
	// to avoid disrupting traffic during a self-update.
	if idle, isErr := u.isGatewayIdle(); !idle {
		if isErr {
			// Track errors and possibly request a clean SuperNode restart
			u.handleGatewayError()
		} else {
			log.Println("Gateway busy, deferring updates")
		}
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
func (u *AutoUpdater) handleGatewayError() {
	const (
		window  = 5 * time.Minute
		retries = 3 // attempts within window before restart
	)
	now := time.Now()
	if u.gwErrWindowStart.IsZero() {
		u.gwErrWindowStart = now
		u.gwErrCount = 1
		log.Printf("Gateway check error (1/%d); starting 5m observation window", retries)
		return
	}

	elapsed := now.Sub(u.gwErrWindowStart)
	if elapsed >= window {
		// Window elapsed; decide based on accumulated errors
		if u.gwErrCount >= retries {
			marker := filepath.Join(u.homeDir, ".needs_restart")
			if err := os.WriteFile(marker, []byte("gateway-error-recover"), 0644); err != nil {
				log.Printf("Failed to write restart marker after gateway errors: %v", err)
			} else {
				log.Printf("Gateway errors persisted (%d/%d) over >=5m; requesting SuperNode restart to recover gateway", u.gwErrCount, retries)
			}
		}
		// Start a new window beginning now, with this error as the first hit
		u.gwErrWindowStart = now
		u.gwErrCount = 1
		return
	}

	// Still within the window; increment and possibly announce threshold reached
	u.gwErrCount++
	if u.gwErrCount < retries {
		log.Printf("Gateway check error (%d/%d) within 5m; will retry", u.gwErrCount, retries)
		return
	}
	// Threshold reached but do not restart until full window elapses
	remaining := window - elapsed
	log.Printf("Gateway error threshold reached; waiting %s before requesting SuperNode restart", remaining.Truncate(time.Second))
}
