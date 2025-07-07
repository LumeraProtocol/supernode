package cascade

import (
	"context"
	"testing"

	"github.com/LumeraProtocol/supernode/supernode/services/common/base"
	"github.com/LumeraProtocol/supernode/supernode/services/common/supernode"
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

			// CPU checks
			assert.NotEmpty(t, resp.CPU.Usage)
			assert.NotEmpty(t, resp.CPU.Remaining)

			// Memory checks
			assert.True(t, resp.Memory.Total > 0)
			assert.True(t, resp.Memory.Used <= resp.Memory.Total)
			assert.True(t, resp.Memory.UsedPerc >= 0 && resp.Memory.UsedPerc <= 100)

			// Available services check
			assert.Contains(t, resp.AvailableServices, "cascade")

			// Task count check - look for cascade service in the services list
			var cascadeService *supernode.ServiceTasks
			for _, service := range resp.Services {
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
