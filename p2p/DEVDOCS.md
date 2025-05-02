# Lumera P2P Service

A Kademlia-based distributed hash table (DHT) implementation that provides decentralized storage and retrieval capabilities for the Lumera network.

## Overview

The P2P service enables supernodes to:
- Store and retrieve data in a distributed network
- Auto-discover other nodes via the Lumera blockchain
- Securely communicate using ALTS (Application Layer Transport Security)
- Replicate data across the network for redundancy

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   P2P API   │────▶│     DHT     │────▶│   Network   │
└─────────────┘     └─────────────┘     └─────────────┘
       │                   │                   │
       │                   │                   │
       ▼                   ▼                   ▼
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ Local Store │     │ Hash Table  │     │ Conn Pool   │
└─────────────┘     └─────────────┘     └─────────────┘
```

- **P2P API**: Public interface for store/retrieve operations
- **DHT**: Core DHT implementation using Kademlia algorithm
- **Network**: Handles peer connections, messaging, and encryption
- **Local Store**: SQLite database for persistent storage
- **Hash Table**: Manages routing table of known peers
- **Conn Pool**: Manages network connections to peers

## Configuration

Key configuration parameters in the YAML config:

```yaml
p2p:
  listen_address: "0.0.0.0"  # Network interface to listen on
  port: 4445                # Port for P2P communication
  data_dir: "~/.lumera/p2p"  # Directory for DHT data storage
  bootstrap_nodes: ""       # Optional comma-separated list of bootstrap nodes
  external_ip: ""           # Optional override for auto-detected external IP
```

### Configuration Field Details

| Field | Description | Default | Required |
|-------|-------------|---------|----------|
| `listen_address` | Network interface to bind to | `0.0.0.0` | Yes |
| `port` | Port to listen for P2P connections | `4445` | Yes |
| `data_dir` | Storage directory for P2P data | N/A | Yes |
| `bootstrap_nodes` | Format: `identity@host:port,identity2@host2:port2` | Auto-fetched from blockchain | No |
| `external_ip` | Public IP address for the node | Auto-detected | No |

The **Node ID** is derived from the Lumera account in your keyring specified by the `key_name` in the supernode config.

## Usage Example

Initializing the P2P service:

```go
// Create P2P configuration
p2pConfig := &p2p.Config{
    ListenAddress:  "0.0.0.0",
    Port:           4445,
    DataDir:        "/path/to/data",
    ID:             supernodeAddress,  // Lumera account address
}

// Initialize P2P service
p2pService, err := p2p.New(ctx, p2pConfig, lumeraClient, keyring, rqStore, nil, nil)
if err != nil {
    return err
}

// Start P2P service
if err := p2pService.Run(ctx); err != nil {
    return err
}
```

Storing and retrieving data:

```go
// Store data - returns base58-encoded key
key, err := p2pService.Store(ctx, []byte("Hello, world!"), 0)
if err != nil {
    return err
}

// Retrieve data
data, err := p2pService.Retrieve(ctx, key)
if err != nil {
    return err
}
```

## Key Components

### Keyring Integration

The P2P service uses the Cosmos SDK keyring for:
- Secure node identity (derived from your Lumera account)
- Cryptographic signatures for secure communication
- Authentication between peers

### Bootstrap Process

When a node starts:
1. It checks for configured bootstrap nodes
2. If none provided, queries the Lumera blockchain for active supernodes
3. Connects to bootstrap nodes and performs iterative `FIND_NODE` queries
4. Builds its routing table based on responses
5. Becomes a full participant in the network

### Data Replication

Data stored in the network is:
1. Stored locally in SQLite
2. Replicated to the closest `Alpha` (6) nodes in the DHT
3. Periodically checked and re-replicated as nodes come and go

## Troubleshooting

- **Can't connect to network**: Verify your `external_ip` is correct or remove it to use auto-detection
- **Bootstrap fails**: Ensure the Lumera client is connected or specify manual bootstrap nodes
- **Storage issues**: Check `data_dir` path and permissions

## Development Notes

- Use `localOnly: true` with `Retrieve()` to only check local storage
- DHT operations use a modified Kademlia with `Alpha=6` for parallelism
- Key format is base58-encoded Blake3 hash of the data