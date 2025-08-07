package cmd

import (
	"fmt"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/utils"
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

	// Get latest release
	release, err := client.GetLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	fmt.Printf("\nLatest release: %s\n", release.TagName)
	fmt.Printf("Current version: %s\n", cfg.Updates.CurrentVersion)

	// Compare versions
	cmp := utils.CompareVersions(cfg.Updates.CurrentVersion, release.TagName)

	if cmp < 0 {
		fmt.Printf("\n✓ Update available: %s → %s\n", cfg.Updates.CurrentVersion, release.TagName)
		fmt.Printf("Published: %s\n", release.PublishedAt.Format("2006-01-02 15:04:05"))

		if release.Body != "" {
			fmt.Println("\nRelease notes:")
			fmt.Println(release.Body)
		}

		fmt.Println("\nTo download this version, run: sn-manager get")
	} else if cmp == 0 {
		fmt.Println("\n✓ You are running the latest version")
	} else {
		fmt.Printf("\n⚠ You are running a newer version than the latest release\n")
	}

	return nil
}
