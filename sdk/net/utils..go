package net

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// GetFreePortInRange finds a free port within the given range
func GetFreePortInRange(ctx context.Context, start, end int) (int, error) {
	for port := start; port <= end; port++ {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			addr := fmt.Sprintf("localhost:%d", port)
			var lc net.ListenConfig
			listener, err := lc.Listen(ctx, "tcp", addr)
			if err == nil {
				listener.Close()
				return port, nil
			}
		}
	}
	return 0, fmt.Errorf("no free port found in range %d-%d", start, end)
}

// AddPortIfMissing adds a default port to an endpoint if no port is specified
func AddPortIfMissing(endpoint string, defaultPort int) string {
	if !strings.Contains(endpoint, ":") {
		return fmt.Sprintf("%s:%d", endpoint, defaultPort)
	}
	return endpoint
}
