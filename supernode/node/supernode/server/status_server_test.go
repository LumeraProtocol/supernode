package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/LumeraProtocol/supernode/pkg/capabilities"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
	"github.com/LumeraProtocol/supernode/supernode/services/common/supernode"
)

func TestSupernodeServer_GetStatus(t *testing.T) {
	ctx := context.Background()

	// Create status service
	statusService := supernode.NewSupernodeStatusService()

	// Create test capabilities
	caps := &capabilities.Capabilities{
		Version:          "1.0.0",
		SupportedActions: []string{"cascade"},
		ActionVersions: map[string][]string{
			"cascade": {"1.0.0"},
		},
		Metadata: map[string]string{
			"test": "true",
		},
		Timestamp: time.Now(),
	}

	// Create server
	server := NewSupernodeServer(statusService, caps)

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
	assert.Empty(t, resp.Services)
	assert.Empty(t, resp.AvailableServices)
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

	// Create test capabilities
	caps := &capabilities.Capabilities{
		Version:          "1.0.0",
		SupportedActions: []string{"cascade"},
		ActionVersions: map[string][]string{
			"cascade": {"1.0.0"},
		},
		Metadata: map[string]string{
			"test": "true",
		},
		Timestamp: time.Now(),
	}

	// Create server
	server := NewSupernodeServer(statusService, caps)

	// Test with service
	resp, err := server.GetStatus(ctx, &pb.StatusRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Should have one service
	assert.Len(t, resp.Services, 1)
	assert.Len(t, resp.AvailableServices, 1)
	assert.Equal(t, []string{"test-service"}, resp.AvailableServices)

	// Check service details
	service := resp.Services[0]
	assert.Equal(t, "test-service", service.ServiceName)
	assert.Equal(t, int32(2), service.TaskCount)
	assert.Equal(t, []string{"task1", "task2"}, service.TaskIds)
}

func TestSupernodeServer_Desc(t *testing.T) {
	statusService := supernode.NewSupernodeStatusService()
	caps := &capabilities.Capabilities{
		Version:          "1.0.0",
		SupportedActions: []string{"cascade"},
		ActionVersions: map[string][]string{
			"cascade": {"1.0.0"},
		},
		Metadata: map[string]string{
			"test": "true",
		},
		Timestamp: time.Now(),
	}
	server := NewSupernodeServer(statusService, caps)

	desc := server.Desc()
	assert.NotNil(t, desc)
	assert.Equal(t, "supernode.SupernodeService", desc.ServiceName)
}

func TestSupernodeServer_GetCapabilities(t *testing.T) {
	ctx := context.Background()

	// Create status service
	statusService := supernode.NewSupernodeStatusService()

	// Create test capabilities
	caps := &capabilities.Capabilities{
		Version:          "1.2.3",
		SupportedActions: []string{"cascade", "sense"},
		ActionVersions: map[string][]string{
			"cascade": {"1.0.0", "1.1.0"},
			"sense":   {"2.0.0"},
		},
		Metadata: map[string]string{
			"build_info": "test-build",
			"features":   "advanced",
		},
		Timestamp: time.Now(),
	}

	// Create server
	server := NewSupernodeServer(statusService, caps)

	// Test GetCapabilities
	resp, err := server.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Capabilities)

	// Check capabilities content
	respCaps := resp.Capabilities
	assert.Equal(t, "1.2.3", respCaps.Version)
	assert.Equal(t, []string{"cascade", "sense"}, respCaps.SupportedActions)
	assert.Equal(t, "test-build", respCaps.Metadata["build_info"])
	assert.Equal(t, "advanced", respCaps.Metadata["features"])
	assert.True(t, respCaps.Timestamp > 0)

	// Check action versions
	assert.Len(t, respCaps.ActionVersions, 2)
	assert.Equal(t, []string{"1.0.0", "1.1.0"}, respCaps.ActionVersions["cascade"].Versions)
	assert.Equal(t, []string{"2.0.0"}, respCaps.ActionVersions["sense"].Versions)
}

func TestSupernodeServer_GetCapabilities_NilCapabilities(t *testing.T) {
	ctx := context.Background()

	// Create status service
	statusService := supernode.NewSupernodeStatusService()

	// Create server with nil capabilities
	server := NewSupernodeServer(statusService, nil)

	// Test GetCapabilities with nil capabilities
	resp, err := server.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "capabilities not initialized")
}
