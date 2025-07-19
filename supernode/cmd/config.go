package cmd

import (
	"github.com/spf13/cobra"
)

// configCmd represents the config command
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
	Long:  `Manage supernode configuration settings.`,
}

func init() {
	rootCmd.AddCommand(configCmd)
}