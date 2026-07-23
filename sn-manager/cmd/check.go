package cmd

import (
	"fmt"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/updater"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check for SuperNode updates",
	Long:  `Check GitHub for new SuperNode releases.`,
	RunE:  runCheck,
}

func runCheck(cmd *cobra.Command, args []string) error {
	// Check if initialized
	if err := checkInitialized(); err != nil {
		return err
	}

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	fmt.Println("Checking for updates...")

	// Create GitHub client
	client := github.NewClient(config.GitHubRepo)

	// Select the same release channel as automatic updates.
	snapshot, err := utils.ReadSupernodeUpdateSnapshot()
	if err != nil {
		return fmt.Errorf("failed to read SuperNode update configuration: %w", err)
	}
	release, err := utils.LatestReleaseForChainID(client, snapshot.ChainID)
	if err != nil {
		return fmt.Errorf("failed to check updates for chain %q: %w", snapshot.ChainID, err)
	}
	managerHome := config.GetManagerHome()
	currentVersion, err := version.NewManager(managerHome).GetCurrentVersion()
	if err != nil {
		return fmt.Errorf("failed to read active SuperNode version: %w", err)
	}

	fmt.Printf("\nLatest release: %s\n", release.TagName)
	fmt.Printf("Current version: %s\n", currentVersion)
	// Report manager version and if it would update under the same policy
	mv := strings.TrimSpace(appVersion)
	if mv != "" && mv != "dev" && !strings.EqualFold(mv, "unknown") {
		managerWould := utils.SameMajor(mv, release.TagName) && utils.CompareVersions(mv, release.TagName) < 0
		fmt.Printf("Manager version: %s (would update: %v)\n", mv, managerWould)
	} else {
		fmt.Printf("Manager version: %s\n", appVersion)
	}

	// Compare versions
	cmp := utils.CompareVersions(currentVersion, release.TagName)

	if cmp < 0 {
		// Use the same logic as auto-updater to determine update eligibility.
		autoUpdater := updater.New(managerHome, cfg, appVersion, nil)
		wouldAutoUpdate := autoUpdater.ShouldUpdate(currentVersion, release.TagName)

		if wouldAutoUpdate {
			fmt.Printf("\n✓ Update available: %s → %s\n", currentVersion, release.TagName)
			fmt.Printf("Published: %s\n", release.PublishedAt.Format("2006-01-02 15:04:05"))
			fmt.Println("\n✓ This update will be applied automatically if auto-upgrade is enabled")
			fmt.Println("   Or manually with: sn-manager get")
		} else {
			fmt.Printf("\n⚠ Major update available: %s → %s\n", currentVersion, release.TagName)
			fmt.Printf("Published: %s\n", release.PublishedAt.Format("2006-01-02 15:04:05"))
			fmt.Println("\n⚠ Major version updates require manual installation:")
			fmt.Printf("   sn-manager get %s\n", release.TagName)
			fmt.Printf("   sn-manager use %s\n", release.TagName)
			fmt.Println("\n⚠ Auto-updater will not automatically install major version updates")
		}

		if release.Body != "" {
			fmt.Println("\nRelease notes:")
			fmt.Println(release.Body)
		}
	} else if cmp == 0 {
		fmt.Println("\n✓ You are running the latest release for this chain")
	} else {
		fmt.Printf("\n⚠ You are running a newer version than the latest release for this chain\n")
	}

	return nil
}
