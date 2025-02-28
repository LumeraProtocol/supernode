//go:generate mockgen -destination=client_mock.go -package=client -source=client.go

package client

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/grpclog"

	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/random"
	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
)

const (
	_        = iota
	KB int = 1 << (10 * iota) // 1024
	MB                        // 1048576
	GB                        // 1073741824
)

const (
	defaultTimeout = 30 * time.Second
	defaultConnWaitTime   = 10 * time.Second
	defaultRetryWaitTime = 1 * time.Second
	maxRetries           = 3

	logPrefix = "client"	
)

type grpcClient interface {
	Connect(ctx context.Context, address string, opts *ClientOptions) (*grpc.ClientConn, error)
	GetState(conn *grpc.ClientConn) connectivity.State
	WaitForStateChange(ctx context.Context, conn *grpc.ClientConn, sourceState connectivity.State) bool
	ResetConnectBackoff(conn *grpc.ClientConn)
	MonitorConnectionState(ctx context.Context, conn *grpc.ClientConn, callback func(connectivity.State))
	BuildDialOptions(opts *ClientOptions) []grpc.DialOption
}

type DialOptionBuilder interface {
	buildCallOptions(opts *ClientOptions) []grpc.CallOption
	buildKeepAliveParams(opts *ClientOptions) keepalive.ClientParameters
	buildConnectParams(opts *ClientOptions) grpc.ConnectParams
}

// NewClient(target string, opts ...DialOption) (conn *ClientConn, err error) {
type ConnectionHandler interface {
	configureContext(ctx context.Context) (context.Context, context.CancelFunc)
	attemptConnection(ctx context.Context, target string, opts *ClientOptions) (*grpc.ClientConn, error)
	retryConnection(ctx context.Context, address string, opts *ClientOptions) (*grpc.ClientConn, error)
}

type ClientConn interface {
	GetState() connectivity.State
	WaitForStateChange(ctx context.Context, sourceState connectivity.State) bool
	ResetConnectBackoff()
}

// Client represents a gRPC client with Lumera ALTS credentials
type Client struct {
	creds credentials.TransportCredentials
	builder DialOptionBuilder
	connHandler ConnectionHandler
}

// ClientOptions contains options for creating a new client
type ClientOptions struct {
	// Connection parameters
	MaxRecvMsgSize     int           // Maximum message size the client can receive (in bytes)
	MaxSendMsgSize     int           // Maximum message size the client can send (in bytes)
	InitialWindowSize  int32         // Initial window size for stream flow control
	InitialConnWindowSize int32      // Initial window size for connection flow control
	ConnWaitTime       time.Duration // Maximum time to wait for connection to become ready

	// Keepalive parameters
	KeepAliveTime      time.Duration // Time after which client pings server if there's no activity
	KeepAliveTimeout   time.Duration // Time to wait for ping ack before considering the connection dead
	AllowWithoutStream bool          // Allow pings even when there are no active streams

	// Retry parameters
	MaxRetries         int           // Maximum number of connection retry attempts
	RetryWaitTime     time.Duration // Time to wait between retry attempts
	EnableRetries     bool          // Whether to enable connection retry logic

	// Backoff parameters - controls how retry delays increase over time
	BackoffConfig     *backoff.Config // Configuration for retry backoff strategy

	// Additional options
	UserAgent         string        // User-Agent header value for all requests
	Authority         string        // Value to use as the :authority pseudo-header
	MinConnectTimeout time.Duration // Minimum time to attempt connection before failing
}

// Exponential backoff configuration
// BaseDelay * (Multiplier ^ n) * (1 ± Jitter)
var defaultBackoffConfig = backoff.Config{
	BaseDelay:  1.0 * time.Second, // Initial delay between retries
	Multiplier: 1.6,			   // Factor by which the delay increases
	Jitter:     0.2,			   // Randomness factor to prevent thundering herd
	MaxDelay:   120 * time.Second, // Maximum delay between retries
}

// DefaultClientOptions returns default client options
func DefaultClientOptions() *ClientOptions {
	return &ClientOptions{
		MaxRecvMsgSize:     100 * MB,
		MaxSendMsgSize:     100 * MB, // 100MB
		InitialWindowSize:     (int32)(1 * MB), // 1MB - controls initial frame size for streams
		InitialConnWindowSize: (int32)(1 * MB), // 1MB - controls initial frame size for connection
		ConnWaitTime:       defaultConnWaitTime,
		
		KeepAliveTime:      30 * time.Minute,
		KeepAliveTimeout:   30 * time.Minute,
		AllowWithoutStream: true,

		MaxRetries:         maxRetries,
		RetryWaitTime:     defaultRetryWaitTime,
		EnableRetries:     true,

		BackoffConfig:    &defaultBackoffConfig,

		MinConnectTimeout: 20 * time.Second,
	}
}

// defaultDialOptionsBuilder is the default implementation of DialOptionBuilder
type defaultDialOptionsBuilder struct{}

// defaultConnectionHandler is the default implementation of ConnectionHandler
type defaultConnectionHandler struct{
	client grpcClient
}

// buildCallOptions creates the call options for the gRPC client
func (b *defaultDialOptionsBuilder) buildCallOptions(opts *ClientOptions) []grpc.CallOption {
	return []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(opts.MaxRecvMsgSize),
		grpc.MaxCallSendMsgSize(opts.MaxSendMsgSize),
	}
}

// buildKeepAliveParams creates the keepalive parameters for the gRPC client
func (b *defaultDialOptionsBuilder) buildKeepAliveParams(opts *ClientOptions) keepalive.ClientParameters {
	return keepalive.ClientParameters{
		Time:                opts.KeepAliveTime,
		Timeout:             opts.KeepAliveTimeout,
		PermitWithoutStream: opts.AllowWithoutStream,
	}
}

// buildConnectParams creates the connection parameters
func (b *defaultDialOptionsBuilder) buildConnectParams(opts *ClientOptions) grpc.ConnectParams {
	var backoffConfig backoff.Config
	if opts.BackoffConfig != nil {
		backoffConfig = *opts.BackoffConfig
	} else {
		// Provide a default backoff configuration
		backoffConfig = defaultBackoffConfig
	}

	return grpc.ConnectParams{
		Backoff:           backoffConfig,
		MinConnectTimeout: opts.MinConnectTimeout,
	}
}

// NewClient creates a new gRPC client with the given ALTS credentials
func NewClient(creds credentials.TransportCredentials) *Client {
	client := &Client{
		creds: creds,
		builder: &defaultDialOptionsBuilder{},
	}
	client.connHandler = &defaultConnectionHandler{client}
	return client
}

// NewClientEx allows injecting a custom DialOptionBuilder and/or 
// ConnectionHandler (for testing)
func NewClientEx(creds credentials.TransportCredentials, builder DialOptionBuilder, 
	connHandler ConnectionHandler) *Client {

	if builder == nil {
		builder = &defaultDialOptionsBuilder{}
	}
	client := &Client{
		creds:   creds,
		builder: builder,
	}
	if connHandler == nil {
		connHandler = &defaultConnectionHandler{client}
	}
	client.connHandler = connHandler
	return client
}

// waitForConnection waits for the connection to be ready or fail permanently
// It implements a state machine to handle different gRPC connection states:
// - Ready: Connection is established and ready for use
// - Shutdown: Connection is permanently closed
// - TransientFailure: Temporary failure, might recover
// - Other states (Idle, Connecting): Keep waiting for state change
var waitForConnection = func(ctx context.Context, conn ClientConn, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		state := conn.GetState()
		switch state {
		case connectivity.Ready:
			return nil
		case connectivity.Shutdown:
			return fmt.Errorf("grpc connection is shutdown")
		case connectivity.TransientFailure:
			return fmt.Errorf("grpc connection is in transient failure")
		default:
			// For Idle and Connecting states, wait for state change
			if !conn.WaitForStateChange(timeoutCtx, state) {
				return fmt.Errorf("timeout waiting for grpc connection state change")
			}
		}
	}
}

// BuildDialOptions creates all grpc dial options including credentials
func (c *Client) BuildDialOptions(opts *ClientOptions) []grpc.DialOption {
	if opts == nil {
		opts = DefaultClientOptions()
	}
	dialOpts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(c.builder.buildCallOptions(opts)...),
		grpc.WithKeepaliveParams(c.builder.buildKeepAliveParams(opts)),
		grpc.WithInitialWindowSize(opts.InitialWindowSize),
		grpc.WithInitialConnWindowSize(opts.InitialConnWindowSize),
		grpc.WithConnectParams(c.builder.buildConnectParams(opts)),
	}

	if opts.UserAgent != "" {
		dialOpts = append(dialOpts, grpc.WithUserAgent(opts.UserAgent))
	}
	if opts.Authority != "" {
		dialOpts = append(dialOpts, grpc.WithAuthority(opts.Authority))
	}

	// Add credentials based on environment
	if os.Getenv("INTEGRATION_TEST_ENV") == "true" {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(c.creds))
	}

	return dialOpts
}

// attemptConnection makes a single connection attempt
// The target name syntax is defined in https://github.com/grpc/grpc/blob/master/doc/naming.md.
func (ch *defaultConnectionHandler) attemptConnection(ctx context.Context, target string, opts *ClientOptions) (*grpc.ClientConn, error) {
	dialOpts := ch.client.BuildDialOptions(opts)
	gclient, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc client: %w", err)
	}

	// Start connection attempt
	gclient.Connect()

	// Start connection attempt
	// Wait for connection to be ready
	if err := waitForConnection(ctx, gclient, opts.ConnWaitTime); err != nil {
		gclient.Close()
		log.WithContext(ctx).WithError(err).Error("Connection failed")
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	log.WithContext(ctx).Debugf("Connected to %s", target)
	return gclient, nil
}

// retryConnection attempts to connect with retry logic
func (ch *defaultConnectionHandler) retryConnection(ctx context.Context, address string, opts *ClientOptions) (*grpc.ClientConn, error) {
	var lastErr error
	
	for retries := 0; retries < opts.MaxRetries; retries++ {
		conn, err := ch.attemptConnection(ctx, address, opts)
		if err == nil {
			return conn, nil
		}
		
		lastErr = err
		
		// Check if we should retry
		if !opts.EnableRetries || retries >= opts.MaxRetries-1 {
			break
		}

		// Wait before retry, respecting context cancellation
		select {
		case <-ctx.Done():
			log.WithContext(ctx).Debugf("Disconnected %s", address)
			return nil, ctx.Err()
		case <-time.After(opts.RetryWaitTime):
			log.WithContext(ctx).Debugf("Retrying connection to %s (attempt #%d)", address, retries+1)
			continue
		}
	}

	return nil, fmt.Errorf("connection failed after %d attempts: %w", opts.MaxRetries, lastErr)
}

// configureContext ensures the context has a timeout and sets up logging
func (ch *defaultConnectionHandler) configureContext(ctx context.Context) (context.Context, context.CancelFunc) {
	grpclog.SetLoggerV2(log.NewLoggerWithErrorLevel())

	id, _ := random.String(8, random.Base62Chars)
	ctx = log.ContextWithPrefix(ctx, fmt.Sprintf("%s-%s", logPrefix, id))

	if _, ok := ctx.Deadline(); !ok {
		return context.WithTimeout(ctx, defaultTimeout)
	}
	return ctx, func() {}
}

// Connect establishes a connection to the server at the given address.
// It implements a retry mechanism with exponential backoff and handles
// both connection establishment and verification of connection readiness.
// address format is "remoteIdentity@address", where:
// - remoteIdentity is the Cosmos address of the remote node
// - address is a standard gRPC address (e.g., "localhost:50051")
func (c *Client) Connect(ctx context.Context, address string, opts *ClientOptions) (*grpc.ClientConn, error) {
	if opts == nil {
		opts = DefaultClientOptions()
	}
	
	// Extract identity if in Lumera format
	remoteIdentity, grpcAddress, err := ltc.ExtractIdentity(address)
	if err != nil {
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	if remoteIdentity != "" {
		lumeraTC, ok := c.creds.(*ltc.LumeraTC)
		if !ok {
			return nil, fmt.Errorf("invalid credentials type")
		}

		// Set remote identity in credentials
		lumeraTC.SetRemoteIdentity(remoteIdentity)
	}

	// configure timeout and log prefix in a context
	ctx, cancel := c.connHandler.configureContext(ctx)
	defer cancel()

	// Attempt connection with retries
	return c.connHandler.retryConnection(ctx, grpcAddress, opts)
}

// GetState returns the current connection state
func (c *Client) GetState(conn *grpc.ClientConn) connectivity.State {
	return conn.GetState()
}

// WaitForStateChange waits for the connection to change from the given state
// Returns true if the state changed, false if the context expired
func (c *Client) WaitForStateChange(ctx context.Context, conn *grpc.ClientConn, sourceState connectivity.State) bool {
	return conn.WaitForStateChange(ctx, sourceState)
}

// ResetConnectBackoff resets the connection's backoff state
// This is useful when implementing custom retry logic or
// when you want to force an immediate reconnection attempt
func (c *Client) ResetConnectBackoff(conn *grpc.ClientConn) {
	conn.ResetConnectBackoff()
}

// MonitorConnectionState starts monitoring connection state changes
// It calls the provided callback whenever the connection state changes
func (c *Client) MonitorConnectionState(ctx context.Context, conn *grpc.ClientConn, callback func(connectivity.State)) {
	go func() {
		currentState := conn.GetState()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if conn.WaitForStateChange(ctx, currentState) {
					currentState = conn.GetState()
					callback(currentState)
				}
			}
		}
	}()
}

// Example usage of MonitorConnectionState:
/*
client.MonitorConnectionState(ctx, conn, func(state connectivity.State) {
    switch state {
    case connectivity.Ready:
        log.Println("Connection is ready")
    case connectivity.TransientFailure:
        log.Println("Connection failed temporarily")
    case connectivity.Shutdown:
        log.Println("Connection is shut down")
    }
})
*/