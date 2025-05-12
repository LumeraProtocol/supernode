# Lumera Supernode SDK

## Overview

The Lumera Supernode SDK is a comprehensive toolkit for building applications that interact with the Lumera blockchain network. It provides a set of components and interfaces that simplify the process of integrating with Lumera supernodes, managing actions, and processing data through the Lumera Protocol.

This SDK facilitates:
- Creating and managing cascade operations
- Securely communicating with the Lumera blockchain
- Managing asynchronous tasks with progress tracking
- Subscribing to events for real-time updates
- Keyring management for secure key storage

## Table of Contents

- [Installation](#installation)
- [Getting Started](#getting-started)
- [Configuration](#configuration)
- [Key Management](#key-management)
- [Core Components](#core-components)
- [Working with Tasks](#working-with-tasks)
- [Event System](#event-system)
- [Command Line Interface](#command-line-interface)
- [Advanced Features](#advanced-features)
- [Architecture](#architecture)
- [API Reference](#api-reference)
- [Troubleshooting](#troubleshooting)

## Installation

### Prerequisites

- Go version 1.18 or higher
- Access to a Lumera network node

### Installing the SDK

```bash
go get github.com/LumeraProtocol/supernode/sdk
```

## Getting Started

### Creating a Client

The first step is to create a client instance that will be used to interact with the Lumera blockchain:

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/LumeraProtocol/supernode/sdk/action"
    "github.com/LumeraProtocol/supernode/sdk/config"
    "github.com/LumeraProtocol/supernode/sdk/log"
    "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

func main() {
    ctx := context.Background()
    
    // Initialize keyring
    kr, err := keyring.New("lumera", keyring.BackendTest, "/path/to/keyring", nil)
    if err != nil {
        panic(fmt.Errorf("failed to initialize keyring: %w", err))
    }
    
    // Create configuration
    cfg, err := config.New(
        config.WithLocalCosmosAddress("lumera1qv3..."),
        config.WithGRPCAddr("127.0.0.1:9090"),
        config.WithChainID("lumera-testnet"),
        config.WithTimeout(10 * time.Second),
    )
    if err != nil {
        panic(fmt.Errorf("failed to create config: %w", err))
    }
    
    // Create logger
    logger := log.NewNoopLogger() // Replace with your logger implementation
    
    // Create action client
    client, err := action.NewClient(ctx, *cfg, logger, kr)
    if err != nil {
        panic(fmt.Errorf("failed to create client: %w", err))
    }
    
    // Now you can use the client to interact with the Lumera blockchain
}
```

### Performing a Cascade Operation

```go
func performCascade(ctx context.Context, client action.Client, data []byte, actionID string) {
    // Start a cascade operation
    taskID, err := client.StartCascade(ctx, data, actionID)
    if err != nil {
        panic(fmt.Errorf("failed to start cascade: %w", err))
    }
    
    fmt.Printf("Cascade operation started with task ID: %s\n", taskID)
    
    // Subscribe to events to track progress
    client.SubscribeToAllEvents(ctx, func(ctx context.Context, e event.Event) {
        if e.TaskID == taskID {
            fmt.Printf("Event: %s, Status: %s\n", e.Type, e.Data["message"])
            
            // Check if the task is completed
            if e.Type == event.TaskCompleted {
                fmt.Println("Cascade operation completed successfully")
            } else if e.Type == event.TaskFailed {
                fmt.Printf("Cascade operation failed: %s\n", e.Data["error"])
            }
        }
    })
}
```

### Working with Task Events

The SDK provides a robust event system for tracking task progress in real-time:

```go
func trackTaskEvents(ctx context.Context, client action.Client, taskID string) {
    // Subscribe to specific event types
    err := client.SubscribeToEvents(ctx, event.TaskProgressActionVerified, func(ctx context.Context, e event.Event) {
        fmt.Println("Action verification completed!")
    })
    if err != nil {
        panic(fmt.Errorf("failed to subscribe to events: %w", err))
    }
    
    err = client.SubscribeToEvents(ctx, event.TaskProgressSupernodesFound, func(ctx context.Context, e event.Event) {
        // Extract data from the event
        if count, ok := e.Data["count"].(int); ok {
            fmt.Printf("Found %d supernodes for processing\n", count)
        }
    })
    if err != nil {
        panic(fmt.Errorf("failed to subscribe to events: %w", err))
    }
    
    // Subscribe to transaction hash events
    err = client.SubscribeToEvents(ctx, event.TxhasReceived, func(ctx context.Context, e event.Event) {
        if txHash, ok := e.Data["txhash"].(string); ok {
            fmt.Printf("Transaction hash received: %s\n", txHash)
            
            // You can now use this hash to query the blockchain
            fmt.Printf("View transaction: https://explorer.lumera.org/tx/%s\n", txHash)
        }
    })
    if err != nil {
        panic(fmt.Errorf("failed to subscribe to events: %w", err))
    }
    
    // Handle completion or failure
    err = client.SubscribeToEvents(ctx, event.TaskCompleted, func(ctx context.Context, e event.Event) {
        fmt.Printf("Task %s completed successfully at %s\n", e.TaskID, e.Timestamp.Format(time.RFC3339))
    })
    if err != nil {
        panic(fmt.Errorf("failed to subscribe to events: %w", err))
    }
    
    err = client.SubscribeToEvents(ctx, event.TaskFailed, func(ctx context.Context, e event.Event) {
        if errMsg, ok := e.Data["error"].(string); ok {
            fmt.Printf("Task failed: %s\n", errMsg)
        }
    })
    if err != nil {
        panic(fmt.Errorf("failed to subscribe to events: %w", err))
    }
}
```

### Retrieving Task Information

You can retrieve information about a task at any time:

```go
func getTaskInfo(ctx context.Context, client action.Client, taskID string) {
    // Get task information
    taskEntry, found := client.GetTask(ctx, taskID)
    if !found {
        fmt.Printf("Task %s not found\n", taskID)
        return
    }
    
    // Display task information
    fmt.Printf("Task ID: %s\n", taskEntry.TaskID)
    fmt.Printf("Task Type: %s\n", taskEntry.TaskType)
    fmt.Printf("Status: %s\n", taskEntry.Status)
    fmt.Printf("Created: %s\n", taskEntry.CreatedAt.Format(time.RFC3339))
    fmt.Printf("Last Updated: %s\n", taskEntry.LastUpdatedAt.Format(time.RFC3339))
    
    // Check if the task has an error
    if taskEntry.Error != nil {
        fmt.Printf("Error: %s\n", taskEntry.Error)
    }
    
    // Display task events history
    fmt.Println("\nEvent History:")
    for i, e := range taskEntry.Events {
        fmt.Printf("%d. [%s] %s\n", i+1, e.Timestamp.Format(time.RFC3339), e.Type)
        
        // Display event data if any
        if len(e.Data) > 0 {
            fmt.Println("   Data:")
            for k, v := range e.Data {
                fmt.Printf("   - %s: %v\n", k, v)
            }
        }
    }
    
    // Calculate current progress
    if len(taskEntry.Events) > 0 {
        latestEvent := taskEntry.Events[len(taskEntry.Events)-1].Type
        current, total := event.GetTaskProgress(latestEvent)
        fmt.Printf("\nProgress: %d/%d (%.1f%%)\n", current, total, float64(current)/float64(total)*100)
    }
}
```

## Configuration

The SDK uses a structured configuration system that allows you to customize various aspects of its behavior.

### Configuration Options

The main configuration components are:

- **AccountConfig**: Local Cosmos address settings
- **LumeraConfig**: Chain-specific settings (GRPC address, chain ID, timeout)

### Creating a Configuration

```go
cfg, err := config.New(
    config.WithLocalCosmosAddress("lumera1qv3..."), // Your account address
    config.WithGRPCAddr("127.0.0.1:9090"),          // Address of the Lumera gRPC endpoint
    config.WithChainID("lumera-testnet"),           // Chain ID of the network
    config.WithTimeout(10 * time.Second),           // Timeout for network operations
)
```

### Default Values

- Default local Cosmos address: "lumera1qv3"
- Default chain ID: "lumera-testnet"
- Default gRPC address: "127.0.0.1:9090"
- Default timeout: 10 seconds

## Key Management

The SDK provides functionality for managing keys securely using the Cosmos SDK keyring.

### Working with the CLI for Key Management

The SDK includes CLI commands for key management:

#### Adding a New Key

```bash
supernode keys add mykey
```

#### Recovering a Key from Mnemonic

```bash
supernode keys recover mykey
```

#### Listing Keys

```bash
supernode keys list
```

### Programmatic Key Management

```go
// Initialize keyring
kr, err := keyring.New("lumera", keyring.BackendTest, "/path/to/keyring", nil)
if err != nil {
    panic(fmt.Errorf("failed to initialize keyring: %w", err))
}

// Get a key by name
key, err := kr.Key("mykey")
if err != nil {
    panic(fmt.Errorf("key not found: %w", err))
}

// Get address from key
address, err := key.GetAddress()
if err != nil {
    panic(fmt.Errorf("failed to get address: %w", err))
}

fmt.Printf("Address: %s\n", address.String())
```

## Core Components

### Action Client

The `action.Client` interface provides methods for performing actions on the Lumera blockchain:

```go
type Client interface {
    StartCascade(ctx context.Context, data []byte, actionID string) (string, error)
    DeleteTask(ctx context.Context, taskID string) error
    GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool)
    SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) error
    SubscribeToAllEvents(ctx context.Context, handler event.Handler) error
}
```

### Lumera Client

The Lumera adapter provides an interface to interact with the Lumera blockchain:

```go
type Client interface {
    GetAction(ctx context.Context, actionID string) (Action, error)
    GetSupernodes(ctx context.Context, height int64) ([]Supernode, error)
}
```

### Task Manager

The task manager handles the creation and management of tasks:

```go
type Manager interface {
    CreateCascadeTask(ctx context.Context, data []byte, actionID string) (string, error)
    GetTask(ctx context.Context, taskID string) (*TaskEntry, bool)
    DeleteTask(ctx context.Context, taskID string) error
    SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler)
    SubscribeToAllEvents(ctx context.Context, handler event.Handler)
}
```

## Working with Tasks

Tasks are the primary way to perform operations that may take time to complete, such as cascade operations.

### Creating a Task

```go
// Create a cascade task
taskID, err := client.StartCascade(ctx, data, actionID)
if err != nil {
    panic(fmt.Errorf("failed to create task: %w", err))
}
```

### Checking Task Status

```go
taskEntry, found := client.GetTask(ctx, taskID)
if !found {
    fmt.Println("Task not found")
    return
}

fmt.Printf("Task status: %s\n", taskEntry.Status)
```

### Deleting a Task

```go
err := client.DeleteTask(ctx, taskID)
if err != nil {
    panic(fmt.Errorf("failed to delete task: %w", err))
}
```

## Event System

The SDK provides an event system that allows you to track the progress of operations in real-time.

### Event Types

Event types are defined in the `event` package:

```go
const (
    TaskStarted                            EventType = "task.started"
    TaskProgressActionVerified             EventType = "task.progress.action_verified"
    TaskProgressActionVerificationFailed   EventType = "task.progress.action_verification_failed"
    TaskProgressSupernodesFound            EventType = "task.progress.supernode_found"
    TaskProgressSupernodesUnavailable      EventType = "task.progress.supernodes_unavailable"
    // ... many other event types
    TaskCompleted                          EventType = "task.completed"
    TxhasReceived                          EventType = "txhash.received"
    TaskFailed                             EventType = "task.failed"
)
```

### Subscribing to Events

```go
// Subscribe to a specific event type
client.SubscribeToEvents(ctx, event.TaskCompleted, func(ctx context.Context, e event.Event) {
    fmt.Printf("Task %s completed\n", e.TaskID)
})

// Subscribe to all events
client.SubscribeToAllEvents(ctx, func(ctx context.Context, e event.Event) {
    fmt.Printf("Event: %s, Task: %s\n", e.Type, e.TaskID)
})
```

## Command Line Interface

The SDK includes a CLI for various operations.

### Starting the Supernode

```bash
supernode start
```

This command starts the supernode service with the configuration defined in config.yaml.

### Key Management Commands

See the [Key Management](#key-management) section for details.

## Advanced Features

### Secure Communication with Supernodes

The SDK provides secure communication with supernodes using ALTS (Application Layer Transport Security):

```go
// Create client factory
factory := net.NewClientFactory(ctx, logger, kr, net.FactoryConfig{
    LocalCosmosAddress: cfg.Account.LocalCosmosAddress,
})

// Create client for a specific supernode
client, err := factory.CreateClient(ctx, supernode)
if err != nil {
    panic(fmt.Errorf("failed to create client: %w", err))
}
defer client.Close(ctx)

// Check supernode health
resp, err := client.HealthCheck(ctx)
if err != nil {
    panic(fmt.Errorf("health check failed: %w", err))
}

fmt.Printf("Supernode health: %s\n", resp.Status)
```

### RaptorQ Encoding

The SDK uses RaptorQ for efficient data encoding and storage:

```go
// This is handled internally by the cascade operation
taskID, err := client.StartCascade(ctx, data, actionID)
```

## Architecture

The Lumera Supernode SDK follows a modular architecture to provide flexibility and maintainability.

### Core Components

1. **Action Client**: Entry point for application developers
2. **Task Manager**: Handles task creation and lifecycle
3. **Event Bus**: Provides real-time updates on task progress
4. **Network Layer**: Handles communication with supernodes
5. **Lumera Adapter**: Interfaces with the Lumera blockchain
6. **Configuration**: Manages SDK settings

### Data Flow

1. Application creates a cascade operation via the Action Client
2. Task Manager creates and schedules the task
3. The task fetches the action from the blockchain via the Lumera Adapter
4. The task finds suitable supernodes and registers the data
5. Events are emitted throughout the process for tracking progress
6. The task completes or fails, and a final event is emitted

## API Reference

### action.Client

```go
type Client interface {
    // StartCascade initiates a cascade operation
    StartCascade(ctx context.Context, data []byte, actionID string) (string, error)
    
    // DeleteTask removes a task by its ID
    DeleteTask(ctx context.Context, taskID string) error
    
    // GetTask retrieves a task by its ID
    GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool)
    
    // SubscribeToEvents registers a handler for specific event types
    SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) error
    
    // SubscribeToAllEvents registers a handler for all events
    SubscribeToAllEvents(ctx context.Context, handler event.Handler) error
}
```

### task.Manager

```go
type Manager interface {
    // CreateCascadeTask creates a cascade task
    CreateCascadeTask(ctx context.Context, data []byte, actionID string) (string, error)
    
    // GetTask retrieves a task by its ID
    GetTask(ctx context.Context, taskID string) (*TaskEntry, bool)
    
    // DeleteTask removes a task by its ID
    DeleteTask(ctx context.Context, taskID string) error
    
    // SubscribeToEvents registers a handler for specific event types
    SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler)
    
    // SubscribeToAllEvents registers a handler for all events
    SubscribeToAllEvents(ctx context.Context, handler event.Handler)
}
```

### event.Bus

```go
type Bus struct {
    // NewBus creates a new event bus
    NewBus(ctx context.Context, logger log.Logger, maxWorkers int) *Bus
    
    // Subscribe registers a handler for a specific event type
    Subscribe(ctx context.Context, eventType EventType, handler Handler)
    
    // SubscribeAll registers a handler for all event types
    SubscribeAll(ctx context.Context, handler Handler)
    
    // Publish sends an event to all relevant subscribers
    Publish(ctx context.Context, event Event)
}
```

### config.Config

```go
type Config struct {
    // Account configuration
    Account AccountConfig
    
    // Lumera blockchain configuration
    Lumera LumeraConfig
}

// New builds a Config, applying the supplied functional options
func New(opts ...Option) (*Config, error)
```

## Troubleshooting

### Common Issues

#### Connection Errors

If you encounter connection errors when trying to connect to the Lumera blockchain:

1. Check that the gRPC address is correct and reachable
2. Verify that the chain ID matches the network you're trying to connect to
3. Ensure that the timeout is sufficiently long for your network conditions

```go
// Increase timeout for slow networks
cfg, err := config.New(
    config.WithGRPCAddr("127.0.0.1:9090"),
    config.WithChainID("lumera-testnet"),
    config.WithTimeout(30 * time.Second), // Increased timeout
)
```

#### Key Management Issues

If you encounter issues with key management:

1. Ensure that the keyring directory exists and is writable
2. Check that the key name is correct
3. For test backends, ensure that you're using a consistent environment

```bash
# List keys to verify they exist
supernode keys list
```


