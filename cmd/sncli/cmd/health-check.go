package cmd

import (
	"log"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(healthCmd)
}

var healthCmd = &cobra.Command{
	Use:   "health-check",
	Short: "Check Supernode health status",
	Run: func(cmd *cobra.Command, args []string) {
		if err := app.HealthCheck(); err != nil {
			log.Fatal(err)
		}
	},
}