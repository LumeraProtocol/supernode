package cli

import (
	"context"
	"fmt"
)

// GetSupernodeStatus retrieves and displays the status of the supernode
func (c *CLI) GetSupernodeStatus() error {
	c.snClientInit()

	resp, err := c.snClient.GetSupernodeStatus(context.Background())
	if err != nil {
		return fmt.Errorf("Get supernode status failed: %v", err)
	}
	fmt.Println("Supernode Status:")
	fmt.Printf("   Version: %s\n", resp.Version)
	fmt.Printf("   Uptime: %d seconds\n", resp.UptimeSeconds)
	fmt.Printf("   CPU Usage: %.2f%% (%d cores)\n", resp.Resources.Cpu.UsagePercent, resp.Resources.Cpu.Cores)
	fmt.Printf("   Memory: %.2fGB used / %.2fGB total (%.2f%%)\n",
		resp.Resources.Memory.UsedGb, resp.Resources.Memory.TotalGb, resp.Resources.Memory.UsagePercent)

	if len(resp.RegisteredServices) > 0 {
		fmt.Println("   Registered Services:")
		for _, svc := range resp.RegisteredServices {
			fmt.Println("   -", svc)
		}
	}

	fmt.Printf("   Network: %d peers connected\n", resp.Network.PeersCount)
	if resp.Rank > 0 {
		fmt.Printf("   Rank: %d\n", resp.Rank)
	}
	if resp.IpAddress != "" {
		fmt.Printf("   IP Address: %s\n", resp.IpAddress)
	}

	return nil
}
