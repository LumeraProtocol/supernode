package supernode_metrics

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
)

// collectMetrics gathers a snapshot of local health and resource usage and
// converts it into the canonical SupernodeMetrics protobuf message expected
// by the on-chain supernode module.
func (hm *Collector) collectMetrics(ctx context.Context) (sntypes.SupernodeMetrics, error) {
	// Get status from the existing status service.
	statusResp, err := hm.statusService.GetStatus(ctx, false)
	if err != nil {
		return sntypes.SupernodeMetrics{}, fmt.Errorf("failed to get status: %w", err)
	}

	versionParts := parseVersion(hm.version)
	metrics := sntypes.SupernodeMetrics{
		// 1–3: semantic version of the running supernode binary.
		VersionMajor: uint32(versionParts[0]), // 1: version_major
		VersionMinor: uint32(versionParts[1]), // 2: version_minor
		VersionPatch: uint32(versionParts[2]), // 3: version_patch
	}

	if statusResp.Resources != nil && statusResp.Resources.Cpu != nil {
		metrics.CpuCoresTotal = float64(statusResp.Resources.Cpu.Cores) // 4: cpu_cores_total
		metrics.CpuUsagePercent = statusResp.Resources.Cpu.UsagePercent // 5: cpu_usage_percent
	}

	if statusResp.Resources != nil && statusResp.Resources.Memory != nil {
		metrics.MemTotalGb = statusResp.Resources.Memory.TotalGb           // 6: mem_total_gb
		metrics.MemFreeGb = statusResp.Resources.Memory.AvailableGb        // 8: mem_free_gb
		metrics.MemUsagePercent = statusResp.Resources.Memory.UsagePercent // 7: mem_usage_percent

		if metrics.MemUsagePercent == 0 && metrics.MemTotalGb > 0 {
			used := metrics.MemTotalGb - metrics.MemFreeGb
			metrics.MemUsagePercent = (used / metrics.MemTotalGb) * 100
		}
	}

	if statusResp.Resources != nil && len(statusResp.Resources.StorageVolumes) > 0 {
		storage := statusResp.Resources.StorageVolumes[0] // 9–11: first volume is reported
		const bytesToGB = 1024.0 * 1024.0 * 1024.0

		metrics.DiskTotalGb = float64(storage.TotalBytes) / bytesToGB    // 9: disk_total_gb
		metrics.DiskFreeGb = float64(storage.AvailableBytes) / bytesToGB // 11: disk_free_gb
		metrics.DiskUsagePercent = storage.UsagePercent                  // 10: disk_usage_percent

		if metrics.DiskUsagePercent == 0 && storage.TotalBytes > 0 {
			used := storage.TotalBytes - storage.AvailableBytes
			metrics.DiskUsagePercent = float64(used) / float64(storage.TotalBytes) * 100
		}
	}

	// 12: uptime_seconds
	metrics.UptimeSeconds = float64(statusResp.UptimeSeconds)

	if statusResp.Network != nil {
		metrics.PeersCount = uint32(statusResp.Network.PeersCount) // 13: peers_count

		// During integration tests the status service is queried without
		// P2P metrics (includeP2PMetrics=false), which leaves PeersCount at
		// zero. On-chain validation requires peers_count > 0 and would mark
		// test supernodes as POSTPONED immediately, preventing P2P store
		// operations. To keep the metrics pipeline exercised without
		// impacting production behavior, we clamp the value to 1 when
		// running under the integration test harness.
		if metrics.PeersCount == 0 && os.Getenv("INTEGRATION_TEST") == "true" {
			metrics.PeersCount = 1
		}
	}

	// 14: open_ports
	metrics.OpenPorts = hm.openPorts(ctx)

	return metrics, nil
}

// parseVersion extracts the semantic version (major, minor, patch) from a
// supernode version string, tolerating common prefixes/suffixes such as
// "v2.0.1-rc1+meta". Invalid or missing components fall back to 2.0.0.
func parseVersion(version string) [3]int {
	result := [3]int{2, 0, 0} // Default to 2.0.0

	// Clean version string.
	version = strings.TrimPrefix(version, "v")
	if idx := strings.IndexAny(version, "-+"); idx != -1 {
		version = version[:idx]
	}

	parts := strings.Split(version, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		if num, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = num
		}
	}

	return result
}

// openPorts returns tri-state status for the node's well-known service ports.
//
// Ports are only marked OPEN when we have recent evidence of real inbound
// traffic. A port is marked CLOSED only when we have no fresh inbound evidence
// and the deterministic active-probing quorum rules indicate that silence is
// meaningful (i.e. enough assigned probers are alive this epoch).
func (hm *Collector) openPorts(ctx context.Context) []sntypes.PortStatus {
	out := make([]sntypes.PortStatus, 0, 3)

	now := time.Now()
	window := time.Duration(EvidenceWindowSeconds) * time.Second
	if hm.reportInterval > 0 {
		// Ensure evidence doesn't expire between expected metrics reports.
		minWindow := hm.reportInterval * 2
		if minWindow > window {
			window = minWindow
		}
	}

	store := reachability.DefaultStore()
	canInferClosed := hm.silenceImpliesClosed(ctx)

	if hm.grpcPort != 0 {
		state := sntypes.PortState_PORT_STATE_UNKNOWN
		if store != nil && store.IsInboundFresh(reachability.ServiceGRPC, window, now) {
			state = sntypes.PortState_PORT_STATE_OPEN
		} else if store != nil && canInferClosed {
			state = sntypes.PortState_PORT_STATE_CLOSED
		}
		out = append(out, sntypes.PortStatus{Port: uint32(hm.grpcPort), State: state})
	}

	if hm.p2pPort != 0 {
		state := sntypes.PortState_PORT_STATE_UNKNOWN
		if store != nil && store.IsInboundFresh(reachability.ServiceP2P, window, now) {
			state = sntypes.PortState_PORT_STATE_OPEN
		} else if store != nil && canInferClosed {
			state = sntypes.PortState_PORT_STATE_CLOSED
		}
		out = append(out, sntypes.PortStatus{Port: uint32(hm.p2pPort), State: state})
	}

	if hm.gatewayPort != 0 {
		state := sntypes.PortState_PORT_STATE_UNKNOWN
		if store != nil && store.IsInboundFresh(reachability.ServiceGateway, window, now) {
			state = sntypes.PortState_PORT_STATE_OPEN
		} else if store != nil && canInferClosed {
			state = sntypes.PortState_PORT_STATE_CLOSED
		}
		out = append(out, sntypes.PortStatus{Port: uint32(hm.gatewayPort), State: state})
	}

	return out
}
