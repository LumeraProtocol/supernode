package updater

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/supernode/node/supernode/gateway"
	"google.golang.org/protobuf/encoding/protojson"
)

const gatewayTimeout = 15 * time.Second

// gatewayGuardIface provides the minimal surface used by AutoUpdater so tests
// can inject a fake implementation.
type gatewayGuardIface interface {
	isIdle() (bool, bool)
	requestRestartNow(reason string)
}

// gatewayGuard encapsulates gateway idleness checks and restart triggers.
// Usage within updater:
//   - When idle: safe to proceed with updates.
//   - When busy: updater may defer updates for a bounded time.
//   - On errors/unresponsive gateway: updater either proceeds (if updates exist)
//     or requests a clean SuperNode restart via a marker file.
type gatewayGuard struct {
	url     string
	timeout time.Duration
	homeDir string
}

func newGatewayGuard(homeDir string) *gatewayGuard {
	url := fmt.Sprintf("http://localhost:%d/api/v1/status", gateway.DefaultGatewayPort)
	return &gatewayGuard{
		url:     url,
		timeout: gatewayTimeout,
		homeDir: homeDir,
	}
}

// isIdle returns (idle, isError). isError=true means the check failed.
func (g *gatewayGuard) isIdle() (bool, bool) {
	client := &http.Client{Timeout: g.timeout}

	resp, err := client.Get(g.url)
	if err != nil {
		log.Printf("Failed to check gateway status: %v", err)
		return false, true
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Gateway returned status %d, not safe to update", resp.StatusCode)
		return false, true
	}

	var status pb.StatusResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read gateway response: %v", err)
		return false, true
	}
	if err := protojson.Unmarshal(body, &status); err != nil {
		log.Printf("Failed to decode gateway response: %v", err)
		return false, true
	}

	totalTasks := 0
	for _, service := range status.RunningTasks {
		totalTasks += int(service.TaskCount)
	}

	if totalTasks > 0 {
		log.Printf("Gateway busy: %d running tasks", totalTasks)
		return false, false
	}
	return true, false
}

// requestRestartNow writes a restart marker immediately with a reason.
func (g *gatewayGuard) requestRestartNow(reason string) {
	marker := filepath.Join(g.homeDir, ".needs_restart")
	if err := os.WriteFile(marker, []byte(reason), 0644); err != nil {
		log.Printf("Failed to write restart marker: %v", err)
	} else {
		log.Printf("Gateway not responding and no updates available; requesting immediate SuperNode restart")
	}
}
