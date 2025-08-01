package cli

import (
	"context"
	"fmt"
)

func (c *CLI) healthCheck() error {
	c.snClientInit()

	resp, err := c.snClient.HealthCheck(context.Background())
	if err != nil {
		return fmt.Errorf("Supernode health check failed: %v", err)
	}
	fmt.Println("✅ Health status:", resp.Status)
	return nil
}

// getSupernodeStatus retrieves and displays the status of the supernode
func (c *CLI) getSupernodeStatus() error {
	c.snClientInit()

	resp, err := c.snClient.GetSupernodeStatus(context.Background())
	if err != nil {
		return fmt.Errorf("Get supernode status failed: %v", err)
	}
	fmt.Println("Supernode Status:")
	fmt.Printf("   CPU Usage: %s, Remaining: %s\n", resp.CPU.Usage, resp.CPU.Remaining)
	fmt.Printf("   Memory Total: %d, Used: %d, Available: %d, Used%%: %.2f\n",
		resp.Memory.Total, resp.Memory.Used, resp.Memory.Available, resp.Memory.UsedPerc)

	if len(resp.Services) > 0 {
		fmt.Println("   Services:")
		for _, service := range resp.Services {
			fmt.Printf("   - %s (Tasks: %d)\n", service.ServiceName, service.TaskCount)
		}
	}

	if len(resp.AvailableServices) > 0 {
		fmt.Println("   Available Services:")
		for _, svc := range resp.AvailableServices {
			fmt.Println("   -", svc)
		}
	}

	return nil
}
