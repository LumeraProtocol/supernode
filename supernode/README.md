# Lumera Supernode

Lumera Supernode is a companion application for Lumera validators who want to provide cascade, sense, and other services to earn rewards.

## Quick Start

### 1. Install the Supernode

```bash
# Download and install instructions would go here
```

### 2. Configure Your Supernode

Create a `config.yml` file in the same directory as your binary:

```yaml
# Supernode Configuration
supernode:
  key_name: "mykey"  # The name you'll use when creating your key
  identity: ""  # Leave empty, will fill after creating key
  ip_address: "0.0.0.0"
  port: 4444

# Keyring Configuration
keyring:
  backend: "test"  # Options: test, file, os
  dir: "keys"
  password: "keyring-password"  # Only for "file" backend

# P2P Network Configuration
p2p:
  listen_address: "0.0.0.0"
  port: 4445
  data_dir: "data/p2p"
  bootstrap_nodes: ""
  external_ip: ""

# Lumera Chain Configuration
lumera:
  grpc_addr: "localhost:9090"
  chain_id: "lumera"
  timeout: 10

# RaptorQ Configuration
raptorq:
  files_dir: "raptorq_files"
```

### 3. Create or Recover a Key

Create a new key (use the same name specified in your config):
```bash
supernode keys add mykey
```

Or recover an existing key:
```bash
supernode keys recover mykey
```

After running either command, you'll receive an address like `lumera15t2e8gjgmuqtj4jzjqfkf3tf5l8vqw69hmrzmr`.

⚠️ **IMPORTANT:** After generating or recovering a key, you MUST update your `config.yml` with the address:

```yaml
supernode:
  key_name: "mykey"
  identity: "lumera15t2e8gjgmuqtj4jzjqfkf3tf5l8vqw69hmrzmr"  # Update with your generated address
  # ... rest of config
```

### 4. Start Your Supernode

```bash
supernode start
```

## Key Management Commands

List all keys:
```bash
supernode keys list
```

## Configuration Guide

- **key_name**: Must match the name you used when creating your key
- **identity**: Must match the address generated for your key
- **backend**: Use "test" for development, "file" or "os" for production
- **p2p.port**: Default is 4445 - don't change this value
- ⚠️ **IMPORTANT:** Make sure you have sufficient balance in the account to broadcast transactions to the Lumera chain

## Data Storage

All data is stored in `~/.supernode` by default.

## Common Flags

```bash
# Custom config file
-c, --config FILE     Use specific config file

# Custom data directory
-d, --basedir DIR     Use custom base directory instead of ~/.supernode
```