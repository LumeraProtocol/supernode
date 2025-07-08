package testutil

import (
	"fmt"
	"net"
)

// GetFreePortInRange finds a free port within the given range.
func GetFreePortInRange(start, end int) (int, error) {
	for port := start; port <= end; port++ {
		addr := fmt.Sprintf("localhost:%d", port)
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			listener.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found in range %d-%d", start, end)
}
