package supernode

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSupernodeStatusService(t *testing.T) {
	ctx := context.Background()

	t.Run("empty service", func(t *testing.T) {
		statusService := NewSupernodeStatusService(nil, nil, nil)

		resp, err := statusService.GetStatus(ctx, false)
		assert.NoError(t, err)

		// Should have version info
		assert.NotEmpty(t, resp.Version)

		// Should have uptime
		assert.True(t, resp.UptimeSeconds >= 0)

		// Should have CPU and Memory info
		assert.True(t, resp.Resources.CPU.UsagePercent >= 0)
		assert.True(t, resp.Resources.CPU.UsagePercent <= 100)
		assert.True(t, resp.Resources.CPU.Cores >= 0)
		assert.True(t, resp.Resources.Memory.TotalGB > 0)
		assert.True(t, resp.Resources.Memory.UsagePercent >= 0)
		assert.True(t, resp.Resources.Memory.UsagePercent <= 100)

		// Should have hardware summary if cores and memory are available
		if resp.Resources.CPU.Cores > 0 && resp.Resources.Memory.TotalGB > 0 {
			assert.NotEmpty(t, resp.Resources.HardwareSummary)
		}

		// Should have storage info (default root filesystem)
		assert.NotEmpty(t, resp.Resources.Storage)
		assert.Equal(t, "/", resp.Resources.Storage[0].Path)

		// Registered services now populated at server layer; status service leaves empty
		assert.Empty(t, resp.RegisteredServices)

		// Should have default values for new fields
		assert.Equal(t, int32(0), resp.Network.PeersCount)
		assert.Empty(t, resp.Network.PeerAddresses)
		assert.Equal(t, int32(0), resp.Rank)
		assert.Empty(t, resp.IPAddress)
	})
}
