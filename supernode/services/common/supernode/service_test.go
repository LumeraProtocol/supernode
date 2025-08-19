package supernode

import (
	"context"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
	"github.com/stretchr/testify/assert"
)

func TestSupernodeStatusService(t *testing.T) {
	ctx := context.Background()

	t.Run("empty service", func(t *testing.T) {
		statusService := NewSupernodeStatusService(nil, nil, nil)

		resp, err := statusService.GetStatus(ctx)
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

		// Should have empty services list
		assert.Empty(t, resp.RunningTasks)
		assert.Empty(t, resp.RegisteredServices)
		
		// Should have default values for new fields
		assert.Equal(t, int32(0), resp.Network.PeersCount)
		assert.Empty(t, resp.Network.PeerAddresses)
		assert.Equal(t, int32(0), resp.Rank)
		assert.Empty(t, resp.IPAddress)
	})

	t.Run("single service with tasks", func(t *testing.T) {
		statusService := NewSupernodeStatusService(nil, nil, nil)

		// Register a mock task provider
		mockProvider := &common.MockTaskProvider{
			ServiceName: "test-service",
			TaskIDs:     []string{"task1", "task2", "task3"},
		}
		statusService.RegisterTaskProvider(mockProvider)

		resp, err := statusService.GetStatus(ctx)
		assert.NoError(t, err)

		// Should have one service
		assert.Len(t, resp.RunningTasks, 1)
		assert.Len(t, resp.RegisteredServices, 1)
		assert.Equal(t, []string{"test-service"}, resp.RegisteredServices)

		service := resp.RunningTasks[0]
		assert.Equal(t, "test-service", service.ServiceName)
		assert.Equal(t, int32(3), service.TaskCount)
		assert.Equal(t, []string{"task1", "task2", "task3"}, service.TaskIDs)
	})

	t.Run("multiple services", func(t *testing.T) {
		statusService := NewSupernodeStatusService(nil, nil, nil)

		// Register multiple mock task providers
		cascadeProvider := &common.MockTaskProvider{
			ServiceName: "cascade",
			TaskIDs:     []string{"cascade1", "cascade2"},
		}
		senseProvider := &common.MockTaskProvider{
			ServiceName: "sense",
			TaskIDs:     []string{"sense1"},
		}

		statusService.RegisterTaskProvider(cascadeProvider)
		statusService.RegisterTaskProvider(senseProvider)

		resp, err := statusService.GetStatus(ctx)
		assert.NoError(t, err)

		// Should have two services
		assert.Len(t, resp.RunningTasks, 2)
		assert.Len(t, resp.RegisteredServices, 2)
		assert.Contains(t, resp.RegisteredServices, "cascade")
		assert.Contains(t, resp.RegisteredServices, "sense")

		// Check services are present
		serviceMap := make(map[string]ServiceTasks)
		for _, service := range resp.RunningTasks {
			serviceMap[service.ServiceName] = service
		}

		cascade, ok := serviceMap["cascade"]
		assert.True(t, ok)
		assert.Equal(t, int32(2), cascade.TaskCount)
		assert.Equal(t, []string{"cascade1", "cascade2"}, cascade.TaskIDs)

		sense, ok := serviceMap["sense"]
		assert.True(t, ok)
		assert.Equal(t, int32(1), sense.TaskCount)
		assert.Equal(t, []string{"sense1"}, sense.TaskIDs)
	})

	t.Run("service with no tasks", func(t *testing.T) {
		statusService := NewSupernodeStatusService(nil, nil, nil)

		// Register a mock task provider with no tasks
		mockProvider := &common.MockTaskProvider{
			ServiceName: "empty-service",
			TaskIDs:     []string{},
		}
		statusService.RegisterTaskProvider(mockProvider)

		resp, err := statusService.GetStatus(ctx)
		assert.NoError(t, err)

		// Should have one service
		assert.Len(t, resp.RunningTasks, 1)
		assert.Len(t, resp.RegisteredServices, 1)
		assert.Equal(t, []string{"empty-service"}, resp.RegisteredServices)

		service := resp.RunningTasks[0]
		assert.Equal(t, "empty-service", service.ServiceName)
		assert.Equal(t, int32(0), service.TaskCount)
		assert.Empty(t, service.TaskIDs)
	})
}
