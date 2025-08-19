package server

import (
	"testing"

	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// --- Mock service implementing server.service ---
type mockService struct{}

func (m *mockService) Desc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: "test.Service",
		HandlerType: (*interface{})(nil),
		Methods:     []grpc.MethodDesc{},
		Streams:     []grpc.StreamDesc{},
	}
}

func TestNewServer_WithValidConfig(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockKeyring := NewMockKeyring(ctl)
	mockLumeraClient := lumera.NewMockClient(ctl)

	cfg := NewConfig()
	cfg.ListenAddresses = "127.0.0.1"
	s, err := New(cfg, "supernode-test", mockKeyring, mockLumeraClient, &mockService{})
	assert.NoError(t, err)
	assert.NotNil(t, s)
}

func TestNewServer_WithNilConfig(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockKeyring := NewMockKeyring(ctl)
	mockLumeraClient := lumera.NewMockClient(ctl)

	s, err := New(nil, "supernode-test", mockKeyring, mockLumeraClient)
	assert.Nil(t, s)
	assert.EqualError(t, err, "config is nil")
}

func TestSetServiceStatusAndClose(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockKeyring := NewMockKeyring(ctl)
	mockLumeraClient := lumera.NewMockClient(ctl)

	cfg := NewConfig()
	cfg.ListenAddresses = "127.0.0.1"
	s, _ := New(cfg, "test", mockKeyring, mockLumeraClient, &mockService{})
	_ = s.setupGRPCServer()

	s.SetServiceStatus("test.Service", grpc_health_v1.HealthCheckResponse_SERVING)
	s.Close()

	// No assertion — success is no panic / crash on shutdown
}
