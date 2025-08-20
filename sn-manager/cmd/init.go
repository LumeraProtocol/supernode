package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/AlecAivazis/survey/v2"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize sn-manager and SuperNode",
	Long: `Initialize both sn-manager and SuperNode in one step.

This command will:
1. Set up sn-manager configuration and directory structure
2. Download the latest SuperNode binary
3. Initialize SuperNode with your validator configuration

All unrecognized flags are passed through to the supernode init command.`,
	DisableFlagParsing: true, // Allow passing through flags to supernode init
	RunE:               runInit,
}

type initFlags struct {
	force          bool
	checkInterval  int
	autoUpgrade    bool
	nonInteractive bool
	supernodeArgs  []string
}

func parseInitFlags(args []string) *initFlags {
	flags := &initFlags{
		checkInterval: 3600,
		autoUpgrade:   true,
	}

	// Parse flags and filter out sn-manager specific ones
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check-interval":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &flags.checkInterval)
				i++ // Skip the value
			}
		case "--auto-upgrade":
			flags.autoUpgrade = true
		case "--force":
			flags.force = true
		case "-y", "--yes":
			flags.nonInteractive = true
			// Pass through to supernode as well
			flags.supernodeArgs = append(flags.supernodeArgs, args[i])

		default:
			// Pass all other args to supernode
			flags.supernodeArgs = append(flags.supernodeArgs, args[i])
		}
	}

	return flags
}

func promptForManagerConfig(flags *initFlags) error {
	if flags.nonInteractive {
		return nil
	}

	fmt.Println("\n=== sn-manager Configuration ===")

	// Auto-upgrade prompt (defaults to true if skipped)
	autoUpgradeOptions := []string{"Yes (recommended)", "No"}
	var autoUpgradeChoice string
	prompt := &survey.Select{
		Message: "Enable automatic updates?",
		Options: autoUpgradeOptions,
		Default: autoUpgradeOptions[0],
		Help:    "Automatically download and apply updates",
	}
	if err := survey.AskOne(prompt, &autoUpgradeChoice); err != nil {
		return err
	}
	flags.autoUpgrade = (autoUpgradeChoice == autoUpgradeOptions[0])

	// Check interval prompt (only if auto-upgrade is enabled)
	if flags.autoUpgrade {
		var intervalStr string
		inputPrompt := &survey.Input{
			Message: "Update check interval (seconds):",
			Default: "3600",
			Help:    "How often to check for updates (3600 = 1 hour)",
		}
		if err := survey.AskOne(inputPrompt, &intervalStr); err != nil {
			return err
		}
		interval, err := strconv.Atoi(intervalStr)
		if err != nil || interval < 60 {
			fmt.Println("Invalid interval, using default (3600)")
			flags.checkInterval = 3600
		} else {
			flags.checkInterval = interval
		}
	}

	return nil
}

func runInit(cmd *cobra.Command, args []string) error {
	// Parse flags
	flags := parseInitFlags(args)

	// Step 1: Initialize sn-manager
	fmt.Println("Step 1: Initializing sn-manager...")
	managerHome := config.GetManagerHome()
	configPath := filepath.Join(managerHome, "config.yml")

	// Check if already initialized
	if _, err := os.Stat(configPath); err == nil {
		if !flags.force {
			return fmt.Errorf("already initialized at %s. Use --force to re-initialize", managerHome)
		}

		// Force mode: remove existing config file only
		fmt.Printf("Removing existing config file at %s...\n", configPath)
		if err := os.Remove(configPath); err != nil {
			return fmt.Errorf("failed to remove existing config: %w", err)
		}
	}

	// Create directory structure
	dirs := []string{
		managerHome,
		filepath.Join(managerHome, "binaries"),
		filepath.Join(managerHome, "downloads"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Prompt for sn-manager configuration in interactive mode
	if err := promptForManagerConfig(flags); err != nil {
		return fmt.Errorf("configuration prompt failed: %w", err)
	}

	// Create config with values
	cfg := &config.Config{
		Updates: config.UpdateConfig{
			CheckInterval: flags.checkInterval,
			AutoUpgrade:   flags.autoUpgrade,
		},
	}

	// Save initial config
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("✓ sn-manager initialized\n")
	if cfg.Updates.AutoUpgrade {
		fmt.Printf("  Auto-upgrade: enabled (every %d seconds)\n", cfg.Updates.CheckInterval)
	}

	// Step 2: Download latest SuperNode binary
	fmt.Println("\nStep 2: Downloading latest SuperNode binary...")

	versionMgr := version.NewManager(managerHome)
	client := github.NewClient(config.GitHubRepo)

	// Get latest stable release
	release, err := client.GetLatestStableRelease()
	if err != nil {
		return fmt.Errorf("failed to get latest stable release: %w", err)
	}

	targetVersion := release.TagName
	fmt.Printf("Latest version: %s\n", targetVersion)

	// Check if already installed
	if versionMgr.IsVersionInstalled(targetVersion) {
		fmt.Printf("✓ SuperNode %s already installed, skipping download\n", targetVersion)
	} else {
		// Get download URL
		downloadURL, err := client.GetSupernodeDownloadURL(targetVersion)
		if err != nil {
			return fmt.Errorf("failed to get download URL: %w", err)
		}

		// Download to temp file
		tempFile := filepath.Join(managerHome, "downloads", fmt.Sprintf("supernode-%s.tmp", targetVersion))

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
		fmt.Println() // New line after progress

		// Install the version
		if err := versionMgr.InstallVersion(targetVersion, tempFile); err != nil {
			return fmt.Errorf("failed to install version: %w", err)
		}

		// Clean up temp file
		os.Remove(tempFile)
	}

	// Set as current version
	if err := versionMgr.SetCurrentVersion(targetVersion); err != nil {
		return fmt.Errorf("failed to set current version: %w", err)
	}

	// Update config with current version
	cfg.Updates.CurrentVersion = targetVersion
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Printf("✓ SuperNode %s ready\n", targetVersion)

	// Step 3: Initialize SuperNode
	fmt.Println("\nStep 3: Initializing SuperNode...")

	// Check if SuperNode is already initialized
	supernodeConfigPath := filepath.Join(os.Getenv("HOME"), ".supernode", "config.yml")
	if _, err := os.Stat(supernodeConfigPath); err == nil {
		fmt.Println("✓ SuperNode already initialized, skipping initialization")
	} else {
		// Get the managed supernode binary path
		supernodeBinary := filepath.Join(managerHome, "current", "supernode")

		// Pass through user-provided arguments to supernode init
		supernodeArgs := append([]string{"init"}, flags.supernodeArgs...)
		
		supernodeCmd := exec.Command(supernodeBinary, supernodeArgs...)
		supernodeCmd.Stdout = os.Stdout
		supernodeCmd.Stderr = os.Stderr
		supernodeCmd.Stdin = os.Stdin

		// Run supernode init
		if err := supernodeCmd.Run(); err != nil {
			return fmt.Errorf("supernode init failed: %w", err)
		}
	}

	fmt.Println("\n✅ Complete! Both sn-manager and SuperNode have been initialized.")
	fmt.Println("\nYou can now start SuperNode with: sn-manager start")

	return nil
}
