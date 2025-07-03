package common

import "context"

// TaskFactory defines an interface to create a new cascade registration task
//
//go:generate mockgen -destination=mocks/supernode_interfaces_mock.go -package=supernodemocks -source=interfaces.go
type TaskFactory interface {
	NewSupernodeTask() SupernodeTaskService
}

// SupernodeTaskService interface allows to perform supernode actions
type SupernodeTaskService interface {
	HealthCheck(ctx context.Context) (HealthCheckResponse, error)
}

// NewSupernodeTask creates a new task for supernode
func (service *SuperNodeService) NewSupernodeTask() SupernodeTaskService {
	task := NewSuperNodeTask("supernode")
	service.Worker.AddTask(task)
	return task
}
