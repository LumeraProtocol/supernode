package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// Version info passed from main
	appVersion   string
	appGitCommit string
	appBuildTime string
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "sn-manager",
	Short: "SuperNode process manager with automatic updates",
	Long: `sn-manager is a process manager for SuperNode that handles automatic updates.

It manages the SuperNode binary lifecycle, including:
- Starting and stopping the SuperNode process
- Monitoring process health
- Checking for and downloading new versions
- Performing zero-downtime updates
`,
}

// Execute adds all child commands and executes the root command
func Execute(ver, commit, built string) error {
	appVersion = ver
	appGitCommit = commit
	appBuildTime = built

	return rootCmd.Execute()
}

func init() {
	// Add all subcommands
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(useCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(lsRemoteCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(versionCmd)
	// SuperNode group
	rootCmd.AddCommand(supernodeCmd)
}

// versionCmd shows version information
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Display version information for sn-manager.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("SN-Manager Version: %s\n", appVersion)
		fmt.Printf("Git Commit: %s\n", appGitCommit)
		fmt.Printf("Build Time: %s\n", appBuildTime)
	},
}
