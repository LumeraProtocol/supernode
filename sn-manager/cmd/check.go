package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/sn-manager/internal/github"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check for SuperNode updates",
	Long:  `Check GitHub for new SuperNode releases.`,
	RunE:  runCheck,
}

func runCheck(cmd *cobra.Command, args []string) error {
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

	fmt.Println("Checking for updates...")
	
	// Create GitHub client
	client := github.NewClient(cfg.Updates.GitHubRepo)
	
	// Get latest release
	release, err := client.GetLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	fmt.Printf("\nLatest release: %s\n", release.TagName)
	fmt.Printf("Current version: %s\n", cfg.Updates.CurrentVersion)
	
	// Compare versions
	cmp := github.CompareVersions(cfg.Updates.CurrentVersion, release.TagName)
	
	if cmp < 0 {
		fmt.Printf("\n✓ Update available: %s → %s\n", cfg.Updates.CurrentVersion, release.TagName)
		fmt.Printf("Published: %s\n", release.PublishedAt.Format("2006-01-02 15:04:05"))
		
		if release.Body != "" {
			fmt.Println("\nRelease notes:")
			fmt.Println(release.Body)
		}
		
		fmt.Println("\nTo upgrade, run: sn-manager upgrade")
	} else if cmp == 0 {
		fmt.Println("\n✓ You are running the latest version")
	} else {
		fmt.Printf("\n⚠ You are running a newer version than the latest release\n")
	}

	return nil
}