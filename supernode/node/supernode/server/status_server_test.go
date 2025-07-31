package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
	"github.com/LumeraProtocol/supernode/supernode/services/common/supernode"
)

func TestSupernodeServer_GetStatus(t *testing.T) {
	ctx := context.Background()

	// Create status service
	statusService := supernode.NewSupernodeStatusService()

	// Create server
	server := NewSupernodeServer(statusService)

	// Test with empty service
	resp, err := server.GetStatus(ctx, &pb.StatusRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Check basic structure
	assert.NotNil(t, resp.Cpu)
	assert.NotNil(t, resp.Memory)
	assert.NotEmpty(t, resp.Cpu.Usage)
	assert.NotEmpty(t, resp.Cpu.Remaining)
	assert.True(t, resp.Memory.Total > 0)

	// Should have no services initially
	assert.Empty(t, resp.RunningTasks)
	assert.Empty(t, resp.RegisteredServices)
}

func TestSupernodeServer_GetStatusWithService(t *testing.T) {
	ctx := context.Background()

	// Create status service
	statusService := supernode.NewSupernodeStatusService()

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
	statusService := supernode.NewSupernodeStatusService()
	server := NewSupernodeServer(statusService)

	desc := server.Desc()
	assert.NotNil(t, desc)
	assert.Equal(t, "supernode.SupernodeService", desc.ServiceName)
}
