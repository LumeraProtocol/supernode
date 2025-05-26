package cascade

import (
	"context"
)

// TaskFactory defines an interface to create a new cascade registration task
//
//go:generate mockgen -destination=mocks/cascade_interfaces_mock.go -package=cascademocks -source=interfaces.go
type TaskFactory interface {
	NewCascadeRegistrationTask() RegistrationTaskService
}

// RegistrationTaskService interface allows to register a new cascade
type RegistrationTaskService interface {
	Register(ctx context.Context, req *RegisterRequest, send func(resp *RegisterResponse) error) error
	HealthCheck(ctx context.Context) (HealthCheckResponse, error)
}
