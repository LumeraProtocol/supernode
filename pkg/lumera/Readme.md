# Lumera Client

A Go client for interacting with the Lumera blockchain.

## Features

- Connect to Lumera nodes via gRPC
- Interact with all Lumera modules:
  - Action module - manage and query action data
  - SuperNode module - interact with supernodes
  - Transaction module - broadcast and query transactions
  - Node module - query node status and blockchain info
- Configurable connection options
- Clean, modular API design with clear separation of interfaces and implementations

## Installation

```bash
go get github.com/LumeraProtocol/lumera-client
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/LumeraProtocol/lumera-client/client"
)

func main() {
	// Create a context
	ctx := context.Background()

	// Initialize the client with options
	lumeraClient, err := client.NewClient(
		ctx,
		client.WithGRPCAddr("localhost:9090"),
		client.WithChainID("lumera-mainnet"),
		client.WithTimeout(30),
	)
	if err != nil {
		log.Fatalf("Failed to create Lumera client: %v", err)
	}
	defer lumeraClient.Close()

	// Get the latest block height
	latestBlock, err := lumeraClient.Node().GetLatestBlock(ctx)
	if err != nil {
		log.Fatalf("Failed to get latest block: %v", err)
	}
	
	fmt.Printf("Latest block height: %d\n", latestBlock.Block.Header.Height)
}
```

## Examples

The repository includes example applications demonstrating how to use the client:

- **Basic Example**: Shows simple queries and interactions with the Lumera blockchain
- **Advanced Example**: Demonstrates a complete transaction flow with error handling and retries

To run the examples:

```bash
# Build and run the basic example
make run-basic

# Build and run the advanced example
make run-advanced
```

## Project Structure

```
lumera-client/
│    				  # Core client package
│   interface.go      # Client interface definitions
│   client.go         # Client implementation
│   config.go         # Configuration types
│   options.go        # Option functions
│   connection.go     # Connection handling
├── modules/              # Module-specific packages
│   ├── action/           # Action module
│   ├── node/             # Node module
│   ├── supernode/        # SuperNode module
│   └── tx/               # Transaction module
└── examples/             # Example applications
    └── main.go           # Basic usage example

```

## Module Documentation

### Action Module

The Action module allows you to interact with Lumera actions, which are the core data processing units in the Lumera blockchain.

```go
// Get action by ID
action, err := client.Action().GetAction(ctx, "action-id-123")

// Calculate fee for action with specific data size
fee, err := client.Action().GetActionFee(ctx, "1024") // 1KB data
```

### Node Module

The Node module provides information about the blockchain and node status.

```go
// Get latest block
block, err := client.Node().GetLatestBlock(ctx)

// Get specific block by height
block, err := client.Node().GetBlockByHeight(ctx, 1000)

// Get node information
nodeInfo, err := client.Node().GetNodeInfo(ctx)
```

### SuperNode Module

The SuperNode module allows you to interact with Lumera supernodes.

```go
// Get top supernodes for a specific block
topNodes, err := client.SuperNode().GetTopSuperNodesForBlock(ctx, 1000)

// Get specific supernode by address
node, err := client.SuperNode().GetSuperNode(ctx, "validator-address")
```

### Transaction Module

The Transaction module handles transaction broadcasting and querying.

```go
// Broadcast a signed transaction
resp, err := client.Tx().BroadcastTx(ctx, txBytes, sdktx.BroadcastMode_BROADCAST_MODE_SYNC)

// Simulate a transaction
sim, err := client.Tx().SimulateTx(ctx, txBytes)

// Get transaction by hash
tx, err := client.Tx().GetTx(ctx, "tx-hash")
```

