package client

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// mockCredentials implements credentials.TransportCredentials interface for testing
type mockCredentials struct {
	credentials.TransportCredentials
	info credentials.ProtocolInfo
	err  error
}

func (m *mockCredentials) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return rawConn, nil, nil
}

func (m *mockCredentials) ServerHandshake(rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return rawConn, nil, nil
}

func (m *mockCredentials) Info() credentials.ProtocolInfo {
	return m.info
}

func (m *mockCredentials) Clone() credentials.TransportCredentials {
	return &mockCredentials{info: m.info, err: m.err}
}

// setupBufConnDialer creates a bufconn listener and returns a dialer for testing
func setupBufConnDialer() (*bufconn.Listener, func(context.Context, string) (net.Conn, error)) {
	listener := bufconn.Listen(bufSize)
	return listener, func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name  string
		creds credentials.TransportCredentials
	}{
		{
			name:  "with insecure credentials",
			creds: insecure.NewCredentials(),
		},
		{
			name:  "with mock credentials",
			creds: &mockCredentials{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.creds)
			assert.NotNil(t, client)
			assert.Equal(t, tt.creds, client.creds)
		})
	}
}

func TestDefaultClientOptions(t *testing.T) {
	opts := DefaultClientOptions()
	assert.NotNil(t, opts, "ClientOptions should be initialized")
	assert.Equal(t, 100*MB, opts.MaxRecvMsgSize, "MaxRecvMsgSize should be 100 MB")
	assert.Equal(t, 100*MB, opts.MaxSendMsgSize, "MaxSendMsgSize should be 100 MB")
	assert.Equal(t, int32(1*MB), opts.InitialWindowSize, "InitialWindowSize should be 1 MB")
	assert.Equal(t, int32(1*MB), opts.InitialConnWindowSize, "InitialConnWindowSize should be 1 MB")
	assert.Equal(t, defaultConnWaitTime, opts.ConnWaitTime, "ConnWaitTime should be 10 seconds")
	assert.True(t, opts.EnableRetries, "EnableRetries should be true")
	assert.Equal(t, maxRetries, opts.MaxRetries, "MaxRetries should be 5")
}

func TestBuildDialOptions(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBuilder := NewMockDialOptionBuilder(ctrl) // Mock the DialOptionBuilder

	tests := []struct {
		name        string
		opts        *ClientOptions
		envSet      bool
		expectedMin int // Minimum number of DialOptions expected
	}{
		{
			name:        "default options without integration env",
			opts:        DefaultClientOptions(),
			envSet:      false,
			expectedMin: 5, // Default DialOptions: CallOptions, Keepalive, ConnParams, TransportCredentials
		},
		{
			name: "with user agent and authority",
			opts: &ClientOptions{
				UserAgent:      "test-agent",
				Authority:      "test-authority",
				MaxRecvMsgSize: 1024,
				MaxSendMsgSize: 1024,
			},
			envSet:      false,
			expectedMin: 7, // Includes UserAgent and Authority
		},
		{
			name:        "integration environment uses insecure credentials",
			opts:        DefaultClientOptions(),
			envSet:      true,
			expectedMin: 5, // TransportCredentials should be insecure
		},
		{
			name: "custom connection parameters",
			opts: &ClientOptions{
				KeepAliveTime:      60 * time.Second,
				KeepAliveTimeout:   10 * time.Second,
				AllowWithoutStream: false,
			},
			envSet:      false,
			expectedMin: 5, // Ensure keepalive params are set
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envSet {
				os.Setenv("INTEGRATION_TEST_ENV", "true")
				defer os.Unsetenv("INTEGRATION_TEST_ENV")
			}

			// Mock expected values
			mockBuilder.EXPECT().buildCallOptions(tt.opts).Return([]grpc.CallOption{}).AnyTimes()
			mockBuilder.EXPECT().buildKeepAliveParams(tt.opts).Return(keepalive.ClientParameters{}).AnyTimes()
			mockBuilder.EXPECT().buildConnectParams(tt.opts).Return(grpc.ConnectParams{}).AnyTimes()

			// Create client with mocked builder
			client := NewClientEx(insecure.NewCredentials(), mockBuilder, nil)

			// Call the real method
			dialOpts := client.BuildDialOptions(tt.opts)

			// Check if at least the expected number of DialOptions are present
			assert.GreaterOrEqual(t, len(dialOpts), tt.expectedMin, "unexpected number of DialOptions")
		})
	}
}

func TestConnect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHandler := NewMockConnectionHandler(ctrl) // Mock ConnectionHandler
	mockOpts := DefaultClientOptions()
	mockConn := new(grpc.ClientConn) // Fake gRPC connection

	// Mock configureContext to return a valid context
	mockHandler.EXPECT().configureContext(gomock.Any()).Return(context.Background(), func() {}).Times(1)

	// Mock retryConnection to return a valid connection
	mockHandler.EXPECT().retryConnection(gomock.Any(), "fake-address", mockOpts).Return(mockConn, nil).Times(1)

	// Use mocked handler in the Client instance
	client := NewClientEx(insecure.NewCredentials(), nil, mockHandler)

	// Call the real `Connect` method
	ctx := context.Background()
	conn, err := client.Connect(ctx, "fake-address", mockOpts)

	// Assertions
	assert.NoError(t, err, "Connect should not return an error")
	assert.NotNil(t, conn, "Connect should return a valid connection")
}

// TestWaitForConnection tests waitForConnection behavior under different states.
func TestWaitForConnection(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name          string
		initialState  connectivity.State
		finalState    connectivity.State
		timeout       time.Duration
		expectError   bool
		expectedError string
	}{
		{
			name:         "successful connection",
			initialState: connectivity.Connecting,
			finalState:   connectivity.Ready,
			timeout:      2 * time.Second,
			expectError:  false,
		},
		{
			name:          "connection shutdown",
			initialState:  connectivity.Connecting,
			finalState:    connectivity.Shutdown,
			timeout:       2 * time.Second,
			expectError:   true,
			expectedError: "grpc connection is shutdown",
		},
		{
			name:          "transient failure",
			initialState:  connectivity.Connecting,
			finalState:    connectivity.TransientFailure,
			timeout:       2 * time.Second,
			expectError:   true,
			expectedError: "grpc connection is in transient failure",
		},
		{
			name:          "timeout waiting for connection",
			initialState:  connectivity.Connecting,
			finalState:    connectivity.Connecting, // Never changes
			timeout:       500 * time.Millisecond,
			expectError:   true,
			expectedError: "timeout waiting for grpc connection state change",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create a mock ClientConn
			mockConn := NewMockClientConn(ctrl)

			// Mock GetState to return the initial state
			mockConn.EXPECT().GetState().Return(tt.initialState).Times(1)

			// Mock WaitForStateChange
			if tt.initialState != tt.finalState {
				mockConn.EXPECT().WaitForStateChange(gomock.Any(), tt.initialState).
					DoAndReturn(func(_ context.Context, _ connectivity.State) bool {
						time.Sleep(100 * time.Millisecond) // Simulate delay
						return true
					}).Times(1)

				mockConn.EXPECT().GetState().Return(tt.finalState).Times(1)
			} else {
				// Simulate a timeout by making WaitForStateChange return false
				mockConn.EXPECT().WaitForStateChange(gomock.Any(), tt.initialState).
					DoAndReturn(func(_ context.Context, _ connectivity.State) bool {
						time.Sleep(tt.timeout + 100*time.Millisecond) // Simulate longer wait
						return false
					}).Times(1)
			}

			err := waitForConnection(ctx, mockConn, tt.timeout)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDefaultDialOptionsBuilder(t *testing.T) {
	builder := &defaultDialOptionsBuilder{}

	t.Run("buildCallOptions", func(t *testing.T) {
		opts := &ClientOptions{
			MaxRecvMsgSize: 1024,
			MaxSendMsgSize: 2048,
		}

		callOptions := builder.buildCallOptions(opts)

		expected := []grpc.CallOption{
			grpc.MaxCallRecvMsgSize(1024),
			grpc.MaxCallSendMsgSize(2048),
		}

		assert.Equal(t, len(expected), len(callOptions), "Expected number of CallOptions")
	})

	t.Run("buildKeepAliveParams", func(t *testing.T) {
		opts := &ClientOptions{
			KeepAliveTime:      10 * time.Second,
			KeepAliveTimeout:   5 * time.Second,
			AllowWithoutStream: true,
		}

		keepAliveParams := builder.buildKeepAliveParams(opts)

		expected := keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}

		assert.Equal(t, expected, keepAliveParams, "Expected keepalive parameters")
	})

	t.Run("buildConnectParams with custom BackoffConfig", func(t *testing.T) {
		backoffConfig := backoff.Config{
			BaseDelay:  500 * time.Millisecond,
			Multiplier: 1.5,
			Jitter:     0.3,
			MaxDelay:   5 * time.Second,
		}

		opts := &ClientOptions{
			BackoffConfig:     &backoffConfig,
			MinConnectTimeout: 15 * time.Second,
		}

		connectParams := builder.buildConnectParams(opts)

		expected := grpc.ConnectParams{
			Backoff:           backoffConfig,
			MinConnectTimeout: 15 * time.Second,
		}

		assert.Equal(t, expected, connectParams, "Expected custom connection parameters")
	})

	t.Run("buildConnectParams with nil BackoffConfig (default applied)", func(t *testing.T) {
		opts := &ClientOptions{
			BackoffConfig:     nil, // No custom backoff config
			MinConnectTimeout: 20 * time.Second,
		}

		connectParams := builder.buildConnectParams(opts)

		expected := grpc.ConnectParams{
			Backoff:           defaultBackoffConfig, // Should use the default backoff config
			MinConnectTimeout: 20 * time.Second,
		}

		assert.Equal(t, expected, connectParams, "Expected default backoff config to be applied")
	})
}
