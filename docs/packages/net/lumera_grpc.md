# Lumera Protocol gRPC Client/Server Documentation

This document provides a comprehensive guide to using the Lumera Protocol's gRPC client and server implementations, including Transport Credentials, configuration options, and common usage patterns.

## Table of Contents
- [Lumera Protocol gRPC Client/Server Documentation](#lumera-protocol-grpc-clientserver-documentation)
  - [Table of Contents](#table-of-contents)
  - [Overview](#overview)
  - [Transport Credentials](#transport-credentials)
    - [Common Options](#common-options)
    - [Client Credentials](#client-credentials)
    - [Server Credentials](#server-credentials)
  - [gRPC Client](#grpc-client)
    - [Client Configuration Options](#client-configuration-options)
    - [Creating a Client](#creating-a-client)
    - [Connecting to a Server](#connecting-to-a-server)
    - [Connection Monitoring](#connection-monitoring)
  - [gRPC Server](#grpc-server)
    - [Server Configuration Options](#server-configuration-options)
    - [Creating a Server](#creating-a-server)
    - [Registering Services](#registering-services)
    - [Starting the Server](#starting-the-server)
    - [Graceful Shutdown](#graceful-shutdown)
  - [Complete Usage Example](#complete-usage-example)
  - [Best Practices](#best-practices)

## Overview

The Lumera Protocol provides a secure gRPC implementation with custom transport credentials based on Cosmos addresses. The implementation includes:

1. A client package for establishing connections to gRPC servers
2. A server package for hosting gRPC services
3. Custom transport credentials for secure communication using Lumera ALTS protocol
4. Configuration options for fine-tuning both client and server behavior

This implementation provides secure, authenticated communication between Lumera network nodes using Cosmos keypairs.

## Transport Credentials

Lumera's transport credentials provide secure authentication and encryption using Cosmos keypairs. The credentials are implemented through the `LumeraTC` type which satisfies the gRPC `TransportCredentials` interface.

### Common Options

Both client and server credentials share common configuration options:

```go
type CommonOptions struct {
    Keyring        keyring.Keyring        // Cosmos keyring containing identity keys
    LocalIdentity  string                 // Local Cosmos address
    PeerType       securekeyx.PeerType    // Local peer type (Supernode or Simplenode)
    Curve          ecdh.Curve             // ECDH curve for key exchange (defaults to P-256)
}
```

### Client Credentials

Client credentials extend common options with remote identity information:

```go
type ClientOptions struct {
    CommonOptions
    RemoteIdentity string // Remote server's Cosmos address
}
```

To create client credentials:

```go
clientCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
    CommonOptions: credentials.CommonOptions{
        Keyring:       cosmosKeyring,
        LocalIdentity: myCosmosAddress,
        PeerType:      securekeyx.Simplenode,
    },
    RemoteIdentity: serverCosmosAddress,
})
if err != nil {
    // Handle error
}
```

### Server Credentials

Server credentials use the common options:

```go
type ServerOptions struct {
    CommonOptions
}
```

To create server credentials:

```go
serverCreds, err := credentials.NewServerCreds(&credentials.ServerOptions{
    CommonOptions: credentials.CommonOptions{
        Keyring:       cosmosKeyring,
        LocalIdentity: myCosmosAddress,
        PeerType:      securekeyx.Supernode,
    },
})
if err != nil {
    // Handle error
}
```

## gRPC Client

The gRPC client implementation provides connection management, automatic retries, and connection monitoring.

### Client Configuration Options

The `ClientOptions` struct provides extensive configuration for client behavior:

```go
type ClientOptions struct {
    // Connection parameters
    MaxRecvMsgSize     int           // Maximum message size client can receive (bytes)
    MaxSendMsgSize     int           // Maximum message size client can send (bytes)
    InitialWindowSize  int32         // Initial window size for stream flow control
    InitialConnWindowSize int32      // Initial window size for connection flow control
    ConnWaitTime       time.Duration // Maximum time to wait for connection to become ready

    // Keepalive parameters
    KeepAliveTime      time.Duration // Time after which client pings server if there's no activity
    KeepAliveTimeout   time.Duration // Time to wait for ping ack before considering connection dead
    AllowWithoutStream bool          // Allow pings even when there are no active streams

    // Retry parameters
    MaxRetries         int           // Maximum number of connection retry attempts
    RetryWaitTime      time.Duration // Time to wait between retry attempts
    EnableRetries      bool          // Whether to enable connection retry logic

    // Backoff parameters
    BackoffConfig      *backoff.Config // Configuration for retry backoff strategy

    // Additional options
    UserAgent          string        // User-Agent header value
    Authority          string        // Value to use as :authority pseudo-header
    MinConnectTimeout  time.Duration // Minimum time to attempt connection before failing
}
```

Default options provide sensible values for most use cases:

```go
func DefaultClientOptions() *ClientOptions {
    return &ClientOptions{
        MaxRecvMsgSize:     100 * MB,
        MaxSendMsgSize:     100 * MB,
        InitialWindowSize:  (int32)(1 * MB),
        InitialConnWindowSize: (int32)(1 * MB),
        ConnWaitTime:       10 * time.Second,
        
        KeepAliveTime:      30 * time.Minute,
        KeepAliveTimeout:   30 * time.Minute,
        AllowWithoutStream: true,

        MaxRetries:         3,
        RetryWaitTime:      1 * time.Second,
        EnableRetries:      true,

        BackoffConfig:      &defaultBackoffConfig,

        MinConnectTimeout:  20 * time.Second,
    }
}
```

### Creating a Client

Create a client using the provided transport credentials:

```go
grpcClient := client.NewClient(clientCreds)
```

### Connecting to a Server

Connect to a server with custom options:

```go
ctx := context.Background()
opts := client.DefaultClientOptions()
opts.ConnWaitTime = 15 * time.Second  // Customize options as needed

conn, err := grpcClient.Connect(ctx, serverAddress, opts)
if err != nil {
    // Handle connection error
}
defer conn.Close()
```

### Connection Monitoring

Monitor connection state changes:

```go
grpcClient.MonitorConnectionState(ctx, conn, func(state connectivity.State) {
    switch state {
    case connectivity.Ready:
        log.Println("Connection is ready")
    case connectivity.TransientFailure:
        log.Println("Connection failed temporarily")
    case connectivity.Shutdown:
        log.Println("Connection is shut down")
    }
})
```

## gRPC Server

The gRPC server implementation provides service hosting, graceful shutdown, and connection management.

### Server Configuration Options

The `ServerOptions` struct provides extensive configuration for server behavior:

```go
type ServerOptions struct {
    // Connection parameters
    MaxRecvMsgSize        int           // Maximum message size server can receive (bytes)
    MaxSendMsgSize        int           // Maximum message size server can send (bytes)
    InitialWindowSize     int32         // Initial window size for stream flow control
    InitialConnWindowSize int32         // Initial window size for connection flow control
    
    // Server parameters
    MaxConcurrentStreams  uint32        // Maximum number of concurrent streams per connection
    GracefulShutdownTime time.Duration  // Time to wait for graceful shutdown

    // Keepalive parameters
    MaxConnectionIdle     time.Duration // Maximum time a connection can be idle
    MaxConnectionAge      time.Duration // Maximum time a connection can exist
    MaxConnectionAgeGrace time.Duration // Additional time to wait before forcefully closing
    Time                  time.Duration // Time after which server pings client if there's no activity
    Timeout               time.Duration // Time to wait for ping ack before considering connection dead
    MinTime               time.Duration // Minimum time client should wait before sending pings
    PermitWithoutStream   bool          // Allow pings even when there are no active streams

    // Additional options
    NumServerWorkers      uint32        // Number of server workers (0 means default)
    WriteBufferSize       int           // Size of write buffer
    ReadBufferSize        int           // Size of read buffer
}
```

Default options provide sensible values for most use cases:

```go
func DefaultServerOptions() *ServerOptions {
    return &ServerOptions{
        MaxRecvMsgSize:        100 * MB,
        MaxSendMsgSize:        100 * MB,
        InitialWindowSize:     (int32)(1 * MB),
        InitialConnWindowSize: (int32)(1 * MB),
        MaxConcurrentStreams:  1000,
        GracefulShutdownTime:  30 * time.Second,

        MaxConnectionIdle:     2 * time.Hour,
        MaxConnectionAge:      2 * time.Hour,
        MaxConnectionAgeGrace: 1 * time.Hour,
        Time:                  1 * time.Hour,
        Timeout:               30 * time.Minute,
        MinTime:               5 * time.Minute,
        PermitWithoutStream:   true,

        WriteBufferSize:       32 * KB,
        ReadBufferSize:        32 * KB,
    }
}
```

### Creating a Server

Create a server with a name and transport credentials:

```go
grpcServer := server.NewServer("MyServiceNode", serverCreds)
```

### Registering Services

Register gRPC services with the server:

```go
// Register your service
pb.RegisterMyServiceServer(grpcServer, &MyServiceImpl{})

// Optionally register the health service
healthServer := health.NewServer()
grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
```

### Starting the Server

Start serving on a specific address:

```go
ctx := context.Background()
opts := server.DefaultServerOptions()
opts.MaxConnectionIdle = time.Minute  // Customize options as needed

go func() {
    err := grpcServer.Serve(ctx, "localhost:50051", opts)
    if err != nil {
        log.Fatalf("Failed to serve: %v", err)
    }
}()

// If using health service, set serving status after server starts
healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
```

### Graceful Shutdown

Gracefully stop the server:

```go
err := grpcServer.Stop(5 * time.Second)
if err != nil {
    log.Fatalf("Failed to stop server: %v", err)
}
```

## Complete Usage Example

Below is a complete example showing client-server communication with Lumera credentials:

```go
package main

import (
    "context"
    "fmt"
    "time"

    "google.golang.org/grpc/health"
    "google.golang.org/grpc/health/grpc_health_v1"

    "github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
    "github.com/LumeraProtocol/supernode/pkg/net/credentials"
    "github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
    "github.com/LumeraProtocol/supernode/pkg/net/grpc/server"
)

func main() {
    // Set up keyring and accounts (simplified)
    kr := setupKeyring()
    serverAddress := "cosmos1..."
    clientAddress := "cosmos2..."
    
    // Create server credentials
    serverCreds, err := credentials.NewServerCreds(&credentials.ServerOptions{
        CommonOptions: credentials.CommonOptions{
            Keyring:       kr,
            LocalIdentity: serverAddress,
            PeerType:      securekeyx.Supernode,
        },
    })
    if err != nil {
        panic(fmt.Sprintf("Failed to create server credentials: %v", err))
    }

    // Create client credentials
    clientCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
        CommonOptions: credentials.CommonOptions{
            Keyring:       kr,
            LocalIdentity: clientAddress,
            PeerType:      securekeyx.Simplenode,
        },
        RemoteIdentity: serverAddress,
    })
    if err != nil {
        panic(fmt.Sprintf("Failed to create client credentials: %v", err))
    }

    // Start gRPC server
    grpcAddress := "localhost:50051"
    grpcServer := server.NewServer("ExampleServer", serverCreds)
    
    // Register service implementations
    myService := &MyServiceImplementation{}
    RegisterMyServiceServer(grpcServer, myService)
    
    // Register health service
    healthServer := health.NewServer()
    grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

    // Set up server options
    serverOpts := server.DefaultServerOptions()
    serverOpts.MaxConnectionIdle = 5 * time.Minute
    
    // Start server in a goroutine
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    go func() {
        if err := grpcServer.Serve(ctx, grpcAddress, serverOpts); err != nil {
            panic(fmt.Sprintf("Server failed: %v", err))
        }
    }()
    
    // Wait for server to start
    time.Sleep(1 * time.Second)
    healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
    
    // Create client
    grpcClient := client.NewClient(clientCreds)
    
    // Set up client options
    clientOpts := client.DefaultClientOptions()
    clientOpts.ConnWaitTime = 5 * time.Second
    
    // Connect to server
    conn, err := grpcClient.Connect(ctx, grpcAddress, clientOpts)
    if err != nil {
        panic(fmt.Sprintf("Client connection failed: %v", err))
    }
    defer conn.Close()
    
    // Create service client
    serviceClient := NewMyServiceClient(conn)
    
    // Make RPC call
    response, err := serviceClient.MyMethod(ctx, &MyRequest{Message: "Hello!"})
    if err != nil {
        panic(fmt.Sprintf("RPC call failed: %v", err))
    }
    fmt.Printf("Response: %s\n", response.Result)
    
    // Clean up
    conn.Close()
    grpcServer.Stop(5 * time.Second)
}
```

## Best Practices

1. **Connection Management**:
   - Always set appropriate timeouts on context objects
   - Set reasonable `ConnWaitTime` values for client connections
   - Use `MonitorConnectionState` to handle reconnection logic

2. **Message Size Limits**:
   - Set appropriate `MaxSendMsgSize` and `MaxRecvMsgSize` based on your expected payload sizes
   - For large data transfers, consider streaming RPCs instead of unary calls

3. **Keepalive Configuration**:
   - Adjust keepalive parameters based on network stability and expected connection duration
   - Use longer timeouts for stable networks, shorter for unstable ones

4. **Graceful Shutdown**:
   - Always use `grpcServer.Stop()` with an appropriate timeout rather than immediate termination
   - Properly close client connections with `conn.Close()`

5. **Health Checking**:
   - Implement the gRPC health checking protocol
   - Register and configure a health server alongside your services
   - Set appropriate health status after server initialization

6. **Security**:
   - Always use proper credentials in production environments
   - Never use `insecure.NewCredentials()` outside of testing
   - Validate both client and server identities

7. **Error Handling**:
   - Implement proper error propagation and handling
   - Log connection errors with appropriate context
   - Implement retry logic for transient failures