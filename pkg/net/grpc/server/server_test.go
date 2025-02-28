package server

import (
	"context"
	"fmt"
	"os"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/testutil"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const testServerName = "test-server"

func GetTestServerAddress(t *testing.T) string {
	freePort, err := testutil.GetFreePortInRange(55000, 56000)
	require.NoError(t, err, "Failed to get a free port")
	return fmt.Sprintf("127.0.0.1:%d", freePort)
}

func TestServerInitialization(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBuilder := NewMockServerOptionBuilder(ctrl)

	server := NewServerWithBuilder(testServerName, insecure.NewCredentials(), mockBuilder)
	assert.NotNil(t, server, "Server should be initialized")
	assert.NotNil(t, server.builder, "Server should have an option builder")
}

func TestRegisterService(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())

	serviceDesc := &grpc.ServiceDesc{
		ServiceName: "TestService",
	}

	mockService := struct{}{} // Dummy service implementation
	server.RegisterService(serviceDesc, mockService)

	assert.Equal(t, 1, len(server.services), "One service should be registered")
	assert.Equal(t, "TestService", server.services[0].Desc.ServiceName, "Service name should match")
}

func TestDefaultServerOptions(t *testing.T) {
	opts := DefaultServerOptions()
	assert.NotNil(t, opts, "Server options should be initialized")
	assert.Equal(t, 100 * MB, opts.MaxRecvMsgSize, "MaxRecvMsgSize should be 100 MB")
	assert.Equal(t, 100 * MB, opts.MaxSendMsgSize, "MaxSendMsgSize should be 100 MB")
	assert.Equal(t, int32(1 * MB), opts.InitialWindowSize, "InitialWindowSize should be 1 MB")
	assert.Equal(t, int32(1 * MB), opts.InitialConnWindowSize, "InitialConnWindowSize should be 1 MB")
	assert.Equal(t, uint32(1000), opts.MaxConcurrentStreams, "MaxConcurrentStreams should be 1000")
	assert.Equal(t, defaultGracefulShutdownTimeout, opts.GracefulShutdownTime, 
		fmt.Sprintf("GracefulShutdownTimeout should be %v", defaultGracefulShutdownTimeout))
	assert.Equal(t, uint32(0), opts.NumServerWorkers, "NumServerWorkers should be 0")
	assert.Equal(t, 32 * KB, opts.WriteBufferSize, "WriteBufferSize should be 32 KB")
	assert.Equal(t, 32 * KB, opts.ReadBufferSize, "ReadBufferSize should be 32 KB")
	assert.Equal(t, 2 * time.Hour, opts.MaxConnectionIdle, "MaxConnectionIdle should be 2 hours")
	assert.Equal(t, 2 * time.Hour, opts.MaxConnectionAge, "MaxConnectionAge should be 2 hours")
	assert.Equal(t, 1 * time.Hour, opts.MaxConnectionAgeGrace, "MaxConnectionAgeGrace should be 1 hour")
	assert.Equal(t, 1 * time.Hour, opts.Time, "Time should be 1 hour")
	assert.Equal(t, 30 * time.Minute, opts.Timeout, "Timeout should be 30 minutes")
	assert.Equal(t, 5 * time.Minute, opts.MinTime, "MinTime should be 5 minutes")
	assert.True(t, opts.PermitWithoutStream, "PermitWithoutStream should be true")
}

func TestBuildKeepAliveParams(t *testing.T) {
	opts := DefaultServerOptions()
	opts.MaxConnectionIdle = opts.MaxConnectionIdle * 2
	opts.MaxConnectionAge = opts.MaxConnectionAge * 2
	opts.MaxConnectionAgeGrace = opts.MaxConnectionAgeGrace * 2
	opts.Time = opts.Time * 2
	opts.Timeout = opts.Timeout * 2
	builder := &defaultServerOptionBuilder{}

	params := builder.buildKeepAliveParams(opts)
	assert.Equal(t, opts.MaxConnectionIdle, params.MaxConnectionIdle, "MaxConnectionIdle should match")
	assert.Equal(t, opts.MaxConnectionAge, params.MaxConnectionAge, "MaxConnectionAge should match")
	assert.Equal(t, opts.MaxConnectionAgeGrace, params.MaxConnectionAgeGrace, "MaxConnectionAgeGrace should match")
	assert.Equal(t, opts.Time, params.Time, "Time should match")
	assert.Equal(t, opts.Timeout, params.Timeout, "Timeout should match")
}

func TestBuildKeepAlivePolicy(t *testing.T) {
	opts := DefaultServerOptions()
	opts.MinTime = opts.MinTime * 2
	builder := &defaultServerOptionBuilder{}

	policy := builder.buildKeepAlivePolicy(opts)
	assert.Equal(t, opts.MinTime, policy.MinTime, "MinTime should match")
	assert.True(t, opts.PermitWithoutStream, "PermitWithoutStream should match")
}

func TestNewServer(t *testing.T) {
	creds := insecure.NewCredentials()
	server := NewServer(testServerName, creds)
	assert.NotNil(t, server, "Server should be initialized")
	assert.NotNil(t, server.builder, "Server should have an option builder")
	assert.Equal(t, creds, server.creds, "Server should have the correct credentials")
	assert.Equal(t, 0, len(server.services), "Server should have no services registered")
	assert.Equal(t, 0, len(server.listeners), "Server should have no listeners")
	assert.NotNil(t, server.done, "Server should have a done channel")
}

func TestNewServerWithBuilder(t *testing.T) {
	creds := insecure.NewCredentials()
	builder := NewMockServerOptionBuilder(gomock.NewController(t))
	server := NewServerWithBuilder(testServerName, creds, builder)
	assert.NotNil(t, server, "Server should be initialized")
	assert.Equal(t, creds, server.creds, "Server should have the correct credentials")
	assert.Equal(t, builder, server.builder, "Server should have the correct option builder")
}

func TestBuildServerOptions(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	opts := DefaultServerOptions()

	os.Setenv("INTEGRATION_TEST_ENV", "true")
	defer os.Unsetenv("INTEGRATION_TEST_ENV")

	serverOpts := server.buildServerOptions(opts)
	assert.NotNil(t, serverOpts, "Server options should be built")
	assert.Greater(t, len(serverOpts), 0, "Server options should not be empty")

	// Verify options exist by checking function signatures
	containsOption := func(target interface{}) bool {
		for _, opt := range serverOpts {
			if reflect.TypeOf(opt) == reflect.TypeOf(target) {
				return true
			}
		}
		return false
	}

	assert.True(t, containsOption(grpc.MaxRecvMsgSize(opts.MaxRecvMsgSize)), "MaxRecvMsgSize should be included")
	assert.True(t, containsOption(grpc.MaxSendMsgSize(opts.MaxSendMsgSize)), "MaxSendMsgSize should be included")
	assert.True(t, containsOption(grpc.InitialWindowSize(opts.InitialWindowSize)), "InitialWindowSize should be included")
	assert.True(t, containsOption(grpc.InitialConnWindowSize(opts.InitialConnWindowSize)), "InitialConnWindowSize should be included")
}

func TestBuildServerOptionsWithWorkers(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	opts := DefaultServerOptions()
	opts.NumServerWorkers = 5

	serverOpts := server.buildServerOptions(opts)
	assert.NotNil(t, serverOpts, "Server options should be built")
	
	// Check function signature rather than value comparison
	found := false
	for _, opt := range serverOpts {
		if reflect.TypeOf(opt) == reflect.TypeOf(grpc.NumStreamWorkers(opts.NumServerWorkers)) {
			found = true
			break
		}
	}
	assert.True(t, found, "NumStreamWorkers should be included")
}

func TestCreateListener(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	address := GetTestServerAddress(t)

	listener, err := server.createListener(context.Background(), address)
	assert.NoError(t, err, "Listener should be created without error")
	assert.NotNil(t, listener, "Listener should not be nil")
	listener.Close()
}

func TestServe(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	server := NewServer(testServerName, insecure.NewCredentials())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	address := GetTestServerAddress(t)
	opts := DefaultServerOptions()
	go func() {
		err := server.Serve(ctx, address, opts)
		assert.NoError(t, err, "Serve should not return an error")
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()
}

func TestServe_NilContext(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	address := GetTestServerAddress(t)
	opts := DefaultServerOptions()
	err := server.Serve(nil, address, opts)
	assert.Error(t, err, "Serve should return an error if context is nil")
}

func TestServe_NilOptions(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	address := GetTestServerAddress(t)
	go func() {
		err := server.Serve(ctx, address, nil)
		assert.NoError(t, err, "Serve should not return an error if options are nil, using defaults")
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()
}

type TestServiceInterface interface {
	TestMethod(ctx context.Context, req interface{}) (interface{}, error)
}

type TestService struct{}
	
func (TestService) TestMethod(ctx context.Context, req interface{}) (interface{}, error) {
	return nil, nil
}

func TestServe_WithRegisteredServices(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})

	serviceDesc := &grpc.ServiceDesc{
		ServiceName: "TestService",
		HandlerType: (*TestServiceInterface)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "TestMethod",
				Handler: func(interface{}, context.Context, func(interface{}) error, grpc.UnaryServerInterceptor) (interface{}, error) {
					return nil, nil
				},
			},
		},
	}

	mockService := TestService{}
	server.RegisterService(serviceDesc, mockService)

	address := GetTestServerAddress(t)
	opts := DefaultServerOptions()
	go func() {
		err := server.Serve(ctx, address, opts)
		assert.NoError(t, err, "Serve should not return an error with registered services")
		close(done)
	}()

	<-done
	cancel()
}

func TestServe_CreateListenerFailed(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	invalidAddress := "invalid_address"
	opts := DefaultServerOptions()
	err := server.Serve(ctx, invalidAddress, opts)
	assert.Error(t, err, "Serve should return an error if listener creation fails")
}

func TestServe_Failure(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	address := GetTestServerAddress(t)
	opts := DefaultServerOptions()

	// Run the server in a goroutine
	go func() {
		err := server.Serve(ctx, address, opts)
		assert.Error(t, err, "Serve should return an error when the listener is closed unexpectedly")
	}()

	// Wait a bit and then close the listener to trigger a failure
	time.Sleep(500 * time.Millisecond)
	server.listeners[0].Close()
}

func TestStop_NoServer(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	err := server.Stop(2 * time.Second)
	assert.NoError(t, err, "Stop should not return an error")
}

func TestStop_GracefulStop(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	address := GetTestServerAddress(t)
	opts := DefaultServerOptions()
	go func() {
		err := server.Serve(ctx, address, opts)
		assert.NoError(t, err, "Serve should not return an error")
	}()

	time.Sleep(500 * time.Millisecond)
	err := server.Stop(2 * time.Second)
	assert.NoError(t, err, "Stop should not return an error")
}

func TestStop_Timeout(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	address := "127.0.0.1:0"
	opts := DefaultServerOptions()
	go func() {
		err := server.Serve(ctx, address, opts)
		assert.NoError(t, err, "Serve should not return an error")
	}()

	// Wait to ensure server is running
	time.Sleep(500 * time.Millisecond)

	// Attempt to stop the server with a very short timeout
	err := server.Stop(1 * time.Nanosecond)
	assert.Error(t, err, "Stop should return an error if it times out")
}

func TestClose_NoActiveServer(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())

	// Create a test listener
	listener, err := net.Listen("tcp", GetTestServerAddress(t))
	assert.NoError(t, err, "Listener should be created successfully")
	server.listeners = append(server.listeners, listener)

	// Close the server
	err = server.Close()
	assert.NoError(t, err, "Close should not return an error")

	// Ensure listener is closed
	_, err = net.Listen("tcp", listener.Addr().String())
	assert.NoError(t, err, "Port should be available after server is closed")
}

func TestClose(t *testing.T) {
	server := NewServer(testServerName, insecure.NewCredentials())

	// Create a test listener
	listener, err := net.Listen("tcp", GetTestServerAddress(t))
	assert.NoError(t, err, "Listener should be created successfully")
	server.listeners = append(server.listeners, listener)

	// Simulate an active gRPC server
	server.server = grpc.NewServer()

	// Close the server
	err = server.Close()
	assert.NoError(t, err, "Close should not return an error")

	// Ensure the gRPC server is stopped
	assert.Nil(t, server.server, "Server should be nil after closing")

	// Ensure listener is closed
	_, err = net.Listen("tcp", listener.Addr().String())
	assert.NoError(t, err, "Port should be available after server is closed")
}
