# Lumera Supernode

Lumera Supernode is a companion application for Lumera validators who want to provide cascade, sense, and other services to earn rewards.

## Prerequisites

Before installing and running the Lumera Supernode, ensure you have the following prerequisites installed:

### Install Build Essentials

```bash
# Ubuntu/Debian
sudo apt update
sudo apt install build-essential
```

### Enable CGO

CGO must be enabled for the supernode to compile and run properly. Set the environment variable:

```bash
export CGO_ENABLED=1
```

To make this permanent, add it to your shell profile:

```bash
echo 'export CGO_ENABLED=1' >> ~/.bashrc
source ~/.bashrc
```

## Installation

### 1. Download the Binary

Download the latest release from GitHub:

### 2. Create Configuration File

Create a `config.yml` file in your base directory (default: `~/.supernode/config.yml`):

```yaml
# Supernode Configuration
supernode:
  key_name: "mykey"  # The name you'll use when creating your key
  identity: ""       # The address you get back after getting the key
  ip_address: "0.0.0.0"
  port: 4444

# Keyring Configuration
keyring:
  backend: "test"  # Options: test, file, os
  dir: "keys"

# P2P Network Configuration
p2p:
  listen_address: "0.0.0.0"
  port: 4445
  data_dir: "data/p2p"

# Lumera Chain Configuration
lumera:
  grpc_addr: "localhost:9090"
  chain_id: "lumera"

# RaptorQ Configuration
raptorq:
  files_dir: "raptorq_files"
```

## Initialization and Key Management

### Initialize a Supernode

The easiest way to set up a supernode is to use the `init` command, which creates a configuration file and sets up keys in one step:

```bash
supernode init mykey --chain-id lumera
```

This will:
1. Create a `config.yml` file in your base directory (default: `~/.supernode/config.yml`)
2. Generate a new key with the specified name
3. Update the configuration with the key's address
4. Output the key information, including the mnemonic

To recover an existing key during initialization:

```bash
supernode init mykey --recover --mnemonic "your mnemonic words here" --chain-id lumera
```

Additional options:
```bash
# Use a specific keyring backend
supernode init mykey --keyring-backend file --chain-id lumera

# Use a custom keyring directory
supernode init mykey --keyring-dir /path/to/keys --chain-id lumera

# Use a custom base directory
supernode init -d /path/to/basedir mykey --chain-id lumera
```

### Manual Key Management

If you prefer to manage keys manually, you can use the following commands:

#### Create a New Key

Create a new key (use the same name you specified in your config):

```bash
supernode keys add mykey
```

This will output an address like:
```
lumera15t2e8gjgmuqtj4jzjqfkf3tf5l8vqw69hmrzmr
```

#### Recover an Existing Key

If you have an existing mnemonic phrase:

```bash
supernode keys recover mykey <MNEMONIC>  # Use quotes if the mnemonic contains spaces, e.g., "word1 word2 word3"
```

#### List Keys

```bash
supernode keys list
```

#### Update Configuration with Your Address

⚠️ **IMPORTANT:** After manually generating or recovering a key, you MUST update your `config.yml` with the generated address:

```yaml
supernode:
  key_name: "mykey"
  identity: "lumera15t2e8gjgmuqtj4jzjqfkf3tf5l8vqw69hmrzmr"  # Your actual address
  ip_address: "0.0.0.0"
  port: 4444
# ... rest of config
```

Note: This step is done automatically when using the `init` command.

## Running the Supernode

### Start the Supernode

```bash
supernode start
```

### Using Custom Configuration

```bash
# Use specific config file
supernode start -c /path/to/config.yml

# Use custom base directory
supernode start -d /path/to/basedir
```

## Configuration Parameters

| Parameter | Description | Required | Default | Example | Notes |
|-----------|-------------|----------|---------|---------|--------|
| `supernode.key_name` | Name of the key for signing transactions | **Yes** | - | `"mykey"` | Must match the name used with `supernode keys add` |
| `supernode.identity` | Lumera address for this supernode | **Yes** | - | `"lumera15t2e8gjgmuqtj4jzjqfkf3tf5l8vqw69hmrzmr"` | Obtained after creating/recovering a key |
| `supernode.ip_address` | IP address to bind the supernode service | **Yes** | - | `"0.0.0.0"` | Use `"0.0.0.0"` to listen on all interfaces |
| `supernode.port` | Port for the supernode service | **Yes** | - | `4444` | Choose an available port |
| `keyring.backend` | Key storage backend type | **Yes** | - | `"test"` | `"test"` for development, `"file"` for encrypted storage, `"os"` for OS keyring |
| `keyring.dir` | Directory to store keyring files | No | `"keys"` | `"keys"` | Relative paths are appended to basedir, absolute paths used as-is |
| `p2p.listen_address` | IP address for P2P networking | **Yes** | - | `"0.0.0.0"` | Use `"0.0.0.0"` to listen on all interfaces |
| `p2p.port` | P2P communication port | **Yes** | - | `4445` | **Do not change this default value** |
| `p2p.data_dir` | Directory for P2P data storage | No | `"data/p2p"` | `"data/p2p"` | Relative paths are appended to basedir, absolute paths used as-is |
| `lumera.grpc_addr` | gRPC endpoint of Lumera validator node | **Yes** | - | `"localhost:9090"` | Must be accessible from supernode |
| `lumera.chain_id` | Lumera blockchain chain identifier | **Yes** | - | `"lumera"` | Must match the actual chain ID |
| `raptorq.files_dir` | Directory to store RaptorQ files | No | `"raptorq_files"` | `"raptorq_files"` | Relative paths are appended to basedir, absolute paths used as-is |

## Command Line Flags

The supernode binary supports the following command-line flags:

| Flag | Short | Description | Value Type | Example | Default |
|------|-------|-------------|------------|---------|---------|
| `--config` | `-c` | Path to configuration file | String | `-c /path/to/config.yml` | `config.yml` in basedir (`~/.supernode/config.yml`) |
| `--basedir` | `-d` | Base directory for data storage | String | `-d /custom/path` | `~/.supernode` |

### Usage Examples

```bash
# Use default config.yml in basedir (~/.supernode/config.yml), with ~/.supernode as basedir
supernode start

# Use custom config file
supernode start -c /etc/supernode/config.yml
supernode start --config /etc/supernode/config.yml

# Use custom base directory
supernode start -d /opt/supernode
supernode start --basedir /opt/supernode

# Combine flags
supernode start -c /etc/supernode/config.yml -d /opt/supernode
```

⚠️ **CRITICAL: Consistent Flag Usage Across Commands**

If you use custom flags (`--config` or `--basedir`) for key management operations, you **MUST** use the same flags for ALL subsequent commands, including the `start` command. Otherwise, your configuration will break and keys will not be found.

### Additional Important Notes:

- Make sure you have sufficient balance in your Lumera account to broadcast transactions
- The P2P port (4445) should not be changed from the default
- Your `key_name` in the config must match the name you used when creating the key
- Your `identity` in the config must match the address generated for your key
- Ensure your Lumera validator node is running and accessible at the configured gRPC address
