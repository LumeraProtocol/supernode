package cli

import (
	"context"
	"fmt"
)

// HealthCheck performs a health check on the Supernode
func (c *CLI) HealthCheck() error {
	c.snClientInit()

	resp, err := c.snClient.HealthCheck(context.Background())
	if err != nil {
		return fmt.Errorf("Supernode health check failed: %v", err)
	}
	fmt.Println("✅ Health status:", resp.Status)
	return nil
}
