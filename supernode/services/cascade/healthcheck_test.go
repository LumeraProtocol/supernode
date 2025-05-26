package cascade

import (
	"context"
	"testing"

	"github.com/LumeraProtocol/supernode/supernode/services/common"
	"github.com/stretchr/testify/assert"
)

func TestHealthCheck(t *testing.T) {
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
				SuperNodeService: common.NewSuperNodeService(nil),
			}

			var primaryTask *CascadeRegistrationTask

			go func() {
				service.RunHelper(ctx, "node-id", "prefix")
			}()

			// Register tasks
			for i := 0; i < tt.taskCount; i++ {
				task := NewCascadeRegistrationTask(service)
				service.Worker.AddTask(task)
				if i == 0 {
					primaryTask = task
				}
			}

			// Always call HealthCheck from first task (if any), otherwise create a temp one
			if primaryTask == nil {
				primaryTask = NewCascadeRegistrationTask(service)
			}

			resp, err := primaryTask.HealthCheck(ctx)
			if tt.expectErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Memory checks
			assert.True(t, resp.Memory.Total > 0)
			assert.True(t, resp.Memory.Used <= resp.Memory.Total)
			assert.True(t, resp.Memory.UsedPerc >= 0 && resp.Memory.UsedPerc <= 100)

			// Task count check
			assert.Equal(t, tt.expectTasks, len(resp.TasksInProgress))
		})
	}
}
