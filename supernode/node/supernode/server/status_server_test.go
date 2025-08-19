package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/supernode"
)

func TestSupernodeServer_GetStatus(t *testing.T) {
	ctx := context.Background()

	// Create status service
	statusService := supernode.NewSupernodeStatusService(nil, nil, nil)

	// Create server
	server := NewSupernodeServer(statusService)

	// Test with empty service
	resp, err := server.GetStatus(ctx, &pb.StatusRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Check basic structure
	assert.NotNil(t, resp.Resources)
	assert.NotNil(t, resp.Resources.Cpu)
	assert.NotNil(t, resp.Resources.Memory)
	assert.NotNil(t, resp.RunningTasks)
	assert.NotNil(t, resp.RegisteredServices)
	
	// Check version field
	assert.NotEmpty(t, resp.Version)
	
	// Check uptime field
	assert.True(t, resp.UptimeSeconds >= 0)

	// Check CPU metrics
	assert.True(t, resp.Resources.Cpu.UsagePercent >= 0)
	assert.True(t, resp.Resources.Cpu.UsagePercent <= 100)
	assert.True(t, resp.Resources.Cpu.Cores >= 0)

	// Check Memory metrics (now in GB)
	assert.True(t, resp.Resources.Memory.TotalGb > 0)
	assert.True(t, resp.Resources.Memory.UsagePercent >= 0)
	assert.True(t, resp.Resources.Memory.UsagePercent <= 100)
	
	// Check hardware summary
	if resp.Resources.Cpu.Cores > 0 && resp.Resources.Memory.TotalGb > 0 {
		assert.NotEmpty(t, resp.Resources.HardwareSummary)
	}

	// Check Storage (should have default root filesystem)
	assert.NotEmpty(t, resp.Resources.StorageVolumes)
	assert.Equal(t, "/", resp.Resources.StorageVolumes[0].Path)

	// Should have no services initially
	assert.Empty(t, resp.RunningTasks)
	assert.Empty(t, resp.RegisteredServices)
	
	// Check new fields have default values
	assert.NotNil(t, resp.Network)
	assert.Equal(t, int32(0), resp.Network.PeersCount)
	assert.Empty(t, resp.Network.PeerAddresses)
	assert.Equal(t, int32(0), resp.Rank)
	assert.Empty(t, resp.IpAddress)
}

func TestSupernodeServer_GetStatusWithService(t *testing.T) {
	ctx := context.Background()

	// Create status service
	statusService := supernode.NewSupernodeStatusService(nil, nil, nil)

	// Add a mock task provider
	mockProvider := &common.MockTaskProvider{
		ServiceName: "test-service",
		TaskIDs:     []string{"task1", "task2"},
	}
	statusService.RegisterTaskProvider(mockProvider)

	// Create server
	server := NewSupernodeServer(statusService)

	// Test with service
	resp, err := server.GetStatus(ctx, &pb.StatusRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Should have one service
	assert.Len(t, resp.RunningTasks, 1)
	assert.Len(t, resp.RegisteredServices, 1)
	assert.Equal(t, []string{"test-service"}, resp.RegisteredServices)

	// Check service details
	service := resp.RunningTasks[0]
	assert.Equal(t, "test-service", service.ServiceName)
	assert.Equal(t, int32(2), service.TaskCount)
	assert.Equal(t, []string{"task1", "task2"}, service.TaskIds)
}

func TestSupernodeServer_Desc(t *testing.T) {
	statusService := supernode.NewSupernodeStatusService(nil, nil, nil)
	server := NewSupernodeServer(statusService)

	desc := server.Desc()
	assert.NotNil(t, desc)
	assert.Equal(t, "supernode.SupernodeService", desc.ServiceName)
}
