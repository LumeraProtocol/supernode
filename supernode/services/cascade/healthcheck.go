package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

// HealthCheckResponse represents the health check response for cascade service
type HealthCheckResponse = common.StatusResponse

// HealthCheck delegates to the common supernode status service
func (task *CascadeRegistrationTask) HealthCheck(ctx context.Context) (HealthCheckResponse, error) {
	// Create a status service and register the cascade service as a task provider
	statusService := common.NewSupernodeStatusService()
	statusService.RegisterTaskProvider(task.CascadeService)

	// Get the status from the common service
	return statusService.GetStatus(ctx)
}
