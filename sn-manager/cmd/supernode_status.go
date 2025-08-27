package cmd

import "github.com/spf13/cobra"

// supernodeStatusCmd reuses the existing status logic
var supernodeStatusCmd = &cobra.Command{
    Use:   "status",
    Short: "Show SuperNode status",
    Long:  `Display the current status of the managed SuperNode process.`,
    RunE:  runStatus,
}

