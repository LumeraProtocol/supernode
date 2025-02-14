package server

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestServerInitialization(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBuilder := NewMockServerOptionBuilder(ctrl)

	server := NewServerWithBuilder(insecure.NewCredentials(), mockBuilder)
	assert.NotNil(t, server, "Server should be initialized")
	assert.NotNil(t, server.builder, "Server should have an option builder")
}

func TestRegisterService(t *testing.T) {
	server := NewServer(insecure.NewCredentials())

	serviceDesc := &grpc.ServiceDesc{
		ServiceName: "TestService",
	}

	mockService := struct{}{} // Dummy service implementation
	server.RegisterService(serviceDesc, mockService)

	assert.Equal(t, 1, len(server.services), "One service should be registered")
	assert.Equal(t, "TestService", server.services[0].Desc.ServiceName, "Service name should match")
}

