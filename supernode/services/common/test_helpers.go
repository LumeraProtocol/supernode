package common

// MockTaskProvider for testing (exported for use in other packages)
type MockTaskProvider struct {
	ServiceName string
	TaskIDs     []string
}

func (m *MockTaskProvider) GetServiceName() string {
	return m.ServiceName
}

func (m *MockTaskProvider) GetRunningTasks() []string {
	return m.TaskIDs
}
