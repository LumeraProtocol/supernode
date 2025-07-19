package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// Build-time variables set by go build -ldflags
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Display version information for the supernode.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Version: %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
