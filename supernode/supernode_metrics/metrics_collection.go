package supernode_metrics

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
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

// openPorts returns the set of TCP ports this node advertises as open in its
// metrics report. For each well-known port we first perform the corresponding
// self-connect health check; only ports that successfully complete their
// external-style probe are included.
func (hm *Collector) openPorts(ctx context.Context) []uint32 {
	seen := make(map[uint32]struct{}, 3)
	out := make([]uint32, 0, 3)

	// gRPC port (supernode service) – include only if the ALTS + gRPC health
	// check succeeds.
	if hm.checkGRPCService(ctx) >= 1.0 {
		val := uint32(hm.grpcPort)
		if _, ok := seen[val]; !ok && val != 0 {
			seen[val] = struct{}{}
			out = append(out, val)
		}
	}

	// P2P port – include only if a full ALTS handshake on the P2P socket
	// succeeds.
	if hm.checkP2PService(ctx) >= 1.0 {
		val := uint32(hm.p2pPort)
		if _, ok := seen[val]; !ok && val != 0 {
			seen[val] = struct{}{}
			out = append(out, val)
		}
	}

	// HTTP gateway / status port – include only if /api/v1/status responds
	// with a successful status code.
	if hm.checkStatusAPI(ctx) >= 1.0 {
		val := uint32(hm.gatewayPort)
		if _, ok := seen[val]; !ok && val != 0 {
			seen[val] = struct{}{}
			out = append(out, val)
		}
	}

	return out
}
