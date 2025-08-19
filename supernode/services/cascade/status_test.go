package cascade

import (
	"context"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/base"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/supernode"
	"github.com/stretchr/testify/assert"
)

func TestGetStatus(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		taskCount   int
		expectErr   bool
		expectTasks int
	}{
		{
			name:        "no tasks",
			taskCount:   0,
			expectErr:   false,
			expectTasks: 0,
		},
		{
			name:        "one task",
			taskCount:   1,
			expectErr:   false,
			expectTasks: 1,
		},
		{
			name:        "multiple tasks",
			taskCount:   3,
			expectErr:   false,
			expectTasks: 3,
		},
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

			// Registered services check
			assert.Contains(t, resp.RegisteredServices, "cascade")
			
			// Check new fields have default values (since service doesn't have access to P2P/lumera/config)
			assert.Equal(t, int32(0), resp.Network.PeersCount)
			assert.Empty(t, resp.Network.PeerAddresses)
			assert.Equal(t, int32(0), resp.Rank)
			assert.Empty(t, resp.IPAddress)

			// Task count check - look for cascade service in the running tasks list
			var cascadeService *supernode.ServiceTasks
			for _, service := range resp.RunningTasks {
				if service.ServiceName == "cascade" {
					cascadeService = &service
					break
				}
			}

			if tt.expectTasks > 0 {
				assert.NotNil(t, cascadeService, "cascade service should be present")
				assert.Equal(t, tt.expectTasks, int(cascadeService.TaskCount))
				assert.Equal(t, tt.expectTasks, len(cascadeService.TaskIDs))
			} else {
				// If no tasks expected, either no cascade service or empty task count
				if cascadeService != nil {
					assert.Equal(t, 0, int(cascadeService.TaskCount))
				}
			}
		})
	}
}
