package cmd

import (
	"time"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	pingTimeout time.Duration
)

var p2pPingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Check the connectivity to a Supernode's kademlia P2P server",
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// default if not provided
		t := pingTimeout
		if t == 0 {
			t = 10 * time.Second
		}

		// If user supplied positional, parse it; it wins over default
		if len(args) == 1 {
			d, err := time.ParseDuration(args[0])
			if err != nil {
				return fmt.Errorf("invalid timeout %q: %w", args[0], err)
			}
			t = d
		}

		if t <= 0 {
			return fmt.Errorf("timeout must be > 0")
		}

		return app.P2PPing(t)
	},
}

func init() {
	p2pCmd.AddCommand(p2pPingCmd)
	p2pPingCmd.Flags().DurationVar(&pingTimeout, "timeout", 5*time.Second, "Ping timeout")
}
