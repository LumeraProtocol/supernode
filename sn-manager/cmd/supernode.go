package cmd

import (
    "github.com/spf13/cobra"
)

// supernodeCmd is a logical group for SuperNode-specific controls
var supernodeCmd = &cobra.Command{
    Use:   "supernode",
    Short: "Control the managed SuperNode",
    Long:  `Start, stop, and inspect the managed SuperNode without affecting the sn-manager service itself.`,
}

func init() {
    // Attach subcommands under supernode group
    supernodeCmd.AddCommand(supernodeStartCmd)
    supernodeCmd.AddCommand(supernodeStopCmd)
    supernodeCmd.AddCommand(supernodeStatusCmd)
}

