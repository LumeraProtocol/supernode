package cascade

import (
	"context"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/base"
	"github.com/stretchr/testify/assert"
)

func TestGetStatus(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		taskCount int
		expectErr bool
	}{
		{name: "no tasks", taskCount: 0, expectErr: false},
		{name: "one task", taskCount: 1, expectErr: false},
		{name: "multiple tasks", taskCount: 3, expectErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup service and worker
			service := &CascadeService{
				SuperNodeService: base.NewSuperNodeService(nil),
			}

			go func() {
				service.RunHelper(ctx, "node-id", "prefix")
			}()

			// Register tasks
			for i := 0; i < tt.taskCount; i++ {
				task := NewCascadeRegistrationTask(service)
				service.Worker.AddTask(task)
			}

			// Call GetStatus from service
			resp, err := service.GetStatus(ctx)
			if tt.expectErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Version check
			assert.NotEmpty(t, resp.Version)

			// Uptime check
			assert.True(t, resp.UptimeSeconds >= 0)

			// CPU checks
			assert.True(t, resp.Resources.CPU.UsagePercent >= 0)
			assert.True(t, resp.Resources.CPU.UsagePercent <= 100)
			assert.True(t, resp.Resources.CPU.Cores >= 0)

			// Memory checks (now in GB)
			assert.True(t, resp.Resources.Memory.TotalGB > 0)
			assert.True(t, resp.Resources.Memory.UsedGB <= resp.Resources.Memory.TotalGB)
			assert.True(t, resp.Resources.Memory.UsagePercent >= 0 && resp.Resources.Memory.UsagePercent <= 100)

			// Hardware summary check
			if resp.Resources.CPU.Cores > 0 && resp.Resources.Memory.TotalGB > 0 {
				assert.NotEmpty(t, resp.Resources.HardwareSummary)
			}

			// Storage checks - should have default root filesystem
			assert.NotEmpty(t, resp.Resources.Storage)
			assert.Equal(t, "/", resp.Resources.Storage[0].Path)

			// Registered services is populated at server layer; cascade service returns none
			assert.Empty(t, resp.RegisteredServices)

			// Check new fields have default values (since service doesn't have access to P2P/lumera/config)
			assert.Equal(t, int32(0), resp.Network.PeersCount)
			assert.Empty(t, resp.Network.PeerAddresses)
			assert.Equal(t, int32(0), resp.Rank)
			assert.Empty(t, resp.IPAddress)
		})
	}
}
