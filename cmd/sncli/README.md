# sncli - Supernode CLI Client

`sncli` is a lightweight command-line interface for interacting with a Lumera Supernode over secure gRPC. It supports health checks, service discovery, task registration, and status queries.

---

## 🔧 Build Instructions

To build `sncli` from source:

```bash
make build-sncli
```

> The binary will be located at: `release/sncli`

---

## ⚙️ Configuration

**Default config location:** `~/.sncli/config.toml`

You can override the location with `--config <path>` or `SNCLI_CONFIG_PATH`.

```toml
# ~/.sncli/config.toml

# Lumera blockchain connection settings
[lumera]
grpc_addr = "localhost:9090"
chain_id = "lumera-devnet-1"

# Keyring settings for managing keys and identities
[keyring]
backend = "test"                     # "file", "test", or "os"
dir = "~/.lumera"                    # Directory where keyring is stored
key_name = "sncli-account"           # Name of local key
local_address = "lumera1abc..."      # Bech32 address of local account (must exist on-chain)

# Supernode peer information
[supernode]
grpc_endpoint = "127.0.0.1:4444"
p2p_endpoint = "127.0.0.1:4445"
address = "lumera1supernodeabc123"   # Bech32 address of the Supernode
```

> Ensure the `keyring.local_address` exists on-chain (i.e., has received funds or sent a tx).

---

## 🚀 Usage

Run the CLI by calling the built binary with a command:

```bash
sncli [global flags] <command> [command flags] [args...]
```

### Global flags

* `--config string`          Path to config file (default `~/.sncli/config.toml`)
* `--grpc_endpoint string`   Supernode gRPC endpoint
* `--p2p_endpoint string`    Supernode P2P endpoint (Kademlia)
* `--address string`         Supernode Lumera address
* `-h, --help`               Help for `sncli`

### Supported Commands
* `completion`        Generate shell completion
* `get-status`        Query Supernode status (CPU, memory, tasks, peers)
* `health-check`      Check Supernode health status
* `list`              List available gRPC services or methods in a specific service
* `list` `<service>`  List methods in a specific gRPC service
* `p2p`             **Supernode P2P utilities**

## 🌐 P2P command group

The `p2p` group contains tools for interacting with the Supernode’s **Kademlia P2P** service.

### `sncli p2p`

Shows P2P help and available subcommands.

```
Supernode P2P utilities

Usage:
  sncli p2p [command]

Available Commands:
  ping        Check the connectivity to a Supernode's kademlia P2P server

### Example

```bash
./sncli --config ~/.sncli.toml --grpc_endpoint 10.0.0.1:4444 --address lumera1xyzabc get-status
```

### `sncli p2p ping` — check connectivity

Checks TCP reachability and does a lightweight ping of the Supernode’s **P2P** endpoint.

**Usage**

```
sncli p2p ping [timeout]
```

* `timeout` is optional (e.g., `5s`, `1m`). If omitted, a sensible default is used (e.g., `10s`).
* You can also set `--timeout` explicitly (if supported by your build).
* The endpoint can be supplied via `--p2p_endpoint` or taken from the config at `[supernode].p2p_endpoint`.

---

**Examples**

```bash
# Use endpoint from config (~/.sncli/config.toml)
sncli p2p ping

# Override endpoint with a flag
sncli p2p ping --p2p_endpoint 172.18.0.6:4445
# Output:
# P2P is alive (172.18.0.6:4445)

# Use a custom timeout (positional)
sncli p2p ping 3s

# Use a custom timeout (flag)
sncli p2p ping --timeout 30s
```

## 📝 Notes

- `sncli` uses a secure gRPC connection with a handshake based on the Lumera keyring.
- Make sure`sncli-account` has been initialized and exists on the chain.
- Config file path is resolved using this algorithm:
  - First uses --config flag (if provided)
  - Else uses SNCLI_CONFIG_PATH environment variable (if defined)
  - Else defaults to ~/.sncli/config.toml
