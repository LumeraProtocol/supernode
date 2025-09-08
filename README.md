# Lumera Supernode

Lumera Supernode is a companion application for Lumera validators who want to provide cascade, sense, and other services to earn rewards.

## gRPC API

The supernode exposes two main gRPC services:

### SupernodeService

Provides system status and monitoring information.

```protobuf
service SupernodeService {
  rpc GetStatus(StatusRequest) returns (StatusResponse);
}

// Optional request flags
message StatusRequest {
  // When true, the response includes detailed P2P metrics and
  // network peer information. This is gated to avoid heavy work
  // unless explicitly requested.
  bool include_p2p_metrics = 1;
}

message StatusResponse {
  string version = 1;                        // Supernode version
  uint64 uptime_seconds = 2;                 // Uptime in seconds
  
  message Resources {
    message CPU {
      double usage_percent = 1;  // CPU usage percentage (0-100)
      int32 cores = 2;          // Number of CPU cores
    }
    
    message Memory {
      double total_gb = 1;       // Total memory in GB
      double used_gb = 2;        // Used memory in GB
      double available_gb = 3;   // Available memory in GB
      double usage_percent = 4;  // Memory usage percentage (0-100)
    }
    
    message Storage {
      string path = 1;           // Storage path being monitored
      uint64 total_bytes = 2;
      uint64 used_bytes = 3;
      uint64 available_bytes = 4;
      double usage_percent = 5;  // Storage usage percentage (0-100)
    }
    
    CPU cpu = 1;
    Memory memory = 2;
    repeated Storage storage_volumes = 3;
    string hardware_summary = 4;  // Formatted hardware summary (e.g., "8 cores / 32GB RAM")
  }
  
  message ServiceTasks {
    string service_name = 1;
    repeated string task_ids = 2;
    int32 task_count = 3;
  }
  
  message Network {
    int32 peers_count = 1;               // Number of connected peers in P2P network
    repeated string peer_addresses = 2;  // List of connected peer addresses (format: "ID@IP:Port")
  }
  
  Resources resources = 3;
  repeated ServiceTasks running_tasks = 4;  // Services with currently running tasks
  repeated string registered_services = 5;   // All registered/available services
  Network network = 6;                      // P2P network information
  int32 rank = 7;                           // Rank in the top supernodes list (0 if not in top list)
  string ip_address = 8;                    // Supernode IP address with port (e.g., "192.168.1.1:4445")
  
  // Optional detailed P2P metrics (present only when requested)
  message P2PMetrics {
    message DhtMetrics {
      message StoreSuccessPoint {
        int64 time_unix = 1;     // event time (unix seconds)
        int32 requests = 2;      // total node RPCs attempted
        int32 successful = 3;    // successful node RPCs
        double success_rate = 4; // percentage (0-100)
      }

      message BatchRetrievePoint {
        int64 time_unix = 1;     // event time (unix seconds)
        int32 keys = 2;          // keys requested
        int32 required = 3;      // required count
        int32 found_local = 4;   // found locally
        int32 found_network = 5; // found on network
        int64 duration_ms = 6;   // duration in milliseconds
      }

      repeated StoreSuccessPoint store_success_recent = 1;
      repeated BatchRetrievePoint batch_retrieve_recent = 2;
      int64 hot_path_banned_skips = 3;
      int64 hot_path_ban_increments = 4;
    }

    message HandleCounters {
      int64 total = 1;
      int64 success = 2;
      int64 failure = 3;
      int64 timeout = 4;
    }

    message BanEntry {
      string id = 1;
      string ip = 2;
      uint32 port = 3;
      int32 count = 4;
      int64 created_at_unix = 5;
      int64 age_seconds = 6;
    }

    message DatabaseStats {
      double p2p_db_size_mb = 1;
      int64 p2p_db_records_count = 2;
    }

    message DiskStatus {
      double all_mb = 1;
      double used_mb = 2;
      double free_mb = 3;
    }

    DhtMetrics dht_metrics = 1;
    map<string, HandleCounters> network_handle_metrics = 2;
    map<string, int64> conn_pool_metrics = 3;
    repeated BanEntry ban_list = 4;
    DatabaseStats database = 5;
    DiskStatus disk = 6;
  }

  P2PMetrics p2p_metrics = 9;               // Only present when include_p2p_metrics=true
}
```

### CascadeService

Handles cascade operations for data storage and retrieval.

```protobuf
service CascadeService {
  rpc Register (stream RegisterRequest) returns (stream RegisterResponse);
  rpc Download (DownloadRequest) returns (stream DownloadResponse);
}

message RegisterRequest {
  oneof request_type {
    DataChunk chunk = 1;
    Metadata metadata = 2;
  }
}

message DataChunk {
  bytes data = 1;
}

message Metadata {
  string task_id = 1;
  string action_id = 2;
}

message RegisterResponse {
  SupernodeEventType event_type = 1;
  string message = 2;
  string tx_hash = 3;
}

message DownloadRequest {
  string action_id = 1;
  string signature = 2;
}

message DownloadResponse {
  oneof response_type {
    DownloadEvent event = 1;
    DataChunk chunk = 2;
  }
}

message DownloadEvent {
  SupernodeEventType event_type = 1;
  string message = 2;
}

enum SupernodeEventType {
  UNKNOWN = 0;
  ACTION_RETRIEVED = 1;
  ACTION_FEE_VERIFIED = 2;
  TOP_SUPERNODE_CHECK_PASSED = 3;
  METADATA_DECODED = 4;
  DATA_HASH_VERIFIED = 5;
  INPUT_ENCODED = 6;
  SIGNATURE_VERIFIED = 7;
  RQID_GENERATED = 8;
  RQID_VERIFIED = 9;
  FINALIZE_SIMULATED = 10;
  ARTEFACTS_STORED = 11;
  ACTION_FINALIZED = 12;
  ARTEFACTS_DOWNLOADED = 13;
}
```

## HTTP Gateway

See docs/gateway.md for the full gateway guide (endpoints, examples, Swagger links).

### Codec Configuration (fixed policy)

The supernode uses a fixed RaptorQ codec policy (linux/amd64 only):
- Concurrency: 4
- Symbol size: 65535
- Redundancy: 5
- Max memory: detected cgroup/system memory minus 10% headroom

Status includes these effective values under `codec` in `StatusResponse`.
The HTTP gateway also exposes a minimal view at `GET /api/v1/codec` with:
- `symbol_size`, `redundancy`, `max_memory_mb`, `concurrency`, `headroom_pct`, `mem_limit_mb`, `mem_limit_source`.

## CLI Commands

### Core Commands

#### `supernode init`
Initialize a new supernode with interactive setup.

```bash
# Interactive setup
supernode init

# Non-interactive setup with all flags (plain passphrase - use only for testing)
supernode init -y \
  --keyring-backend file \
  --keyring-passphrase "your-secure-passphrase" \
  --key-name mykey \
  --recover \
  --mnemonic "word1 word2 word3 ... word24" \
  --supernode-addr 0.0.0.0 \
  --supernode-port 4444 \
  --lumera-grpc https://grpc.testnet.lumera.io \
  --chain-id lumera-testnet-1

# Non-interactive setup with passphrase from environment variable
export KEYRING_PASS="your-secure-passphrase"
supernode init -y \
  --keyring-backend file \
  --keyring-passphrase-env KEYRING_PASS \
  --key-name mykey \
  --recover \
  --mnemonic "word1 word2 word3 ... word24" \
  --supernode-addr 0.0.0.0 \
  --supernode-port 4444 \
  --lumera-grpc https://grpc.testnet.lumera.io \
  --chain-id lumera-testnet-1

# Non-interactive setup with passphrase from file
echo "your-secure-passphrase" > /path/to/passphrase.txt
supernode init -y \
  --keyring-backend file \
  --keyring-passphrase-file /path/to/passphrase.txt \
  --key-name mykey \
  --recover \
  --mnemonic "word1 word2 word3 ... word24" \
  --supernode-addr 0.0.0.0 \
  --supernode-port 4444 \
  --lumera-grpc https://grpc.testnet.lumera.io \
  --chain-id lumera-testnet-1
```

**Available flags:**
- `--force` - Override existing installation
- `-y`, `--yes` - Skip interactive prompts, use defaults
- `--keyring-backend` - Keyring backend (`os`, `file`, `test`)
- `--key-name` - Name of key to create or recover
- `--recover` - Recover existing key from mnemonic
- `--mnemonic` - Mnemonic phrase for recovery (use with --recover)
- `--supernode-addr` - IP address for supernode service
- `--supernode-port` - Port for supernode service
- `--lumera-grpc` - Lumera gRPC address (host:port)
- `--chain-id` - Lumera blockchain chain ID
- `--keyring-passphrase` - Keyring passphrase for non-interactive mode (plain text)
- `--keyring-passphrase-env` - Environment variable containing keyring passphrase
- `--keyring-passphrase-file` - File containing keyring passphrase

#### `supernode start`
Start the supernode service.

```bash
supernode start                   # Use default config directory
supernode start -d /path/to/dir   # Use custom base directory
```

#### `supernode version`
Display version information.

```bash
supernode version
```

### Key Management

#### `supernode keys list`
List all keys in the keyring with addresses.

```bash
supernode keys list
```

#### `supernode keys recover [name]`
Recover key from existing mnemonic phrase.

```bash
supernode keys recover mykey
supernode keys recover mykey --mnemonic "word1 word2..."
```

### Configuration Management

#### `supernode config list`
Display current configuration parameters.

```bash
supernode config list
```

#### `supernode config update`
Interactive configuration parameter updates.

```bash
supernode config update
```

### Global Flags

#### `--basedir, -d`
Specify custom base directory (default: `~/.supernode`).

```bash
supernode start -d /custom/path
supernode config list -d /custom/path
```

## Configuration

The configuration file is located at `~/.supernode/config.yml` and contains the following sections:

### Supernode Configuration
```yaml
supernode:
  key_name: "mykey"                    # Name of the key for signing transactions
  identity: "lumera15t2e8gjgmuqtj..."  # Lumera address for this supernode  
  host: "0.0.0.0"                     # Host address to bind the service (IP or hostname)
  port: 4444                          # Port for the supernode service
```

### Keyring Configuration
```yaml
keyring:
  backend: "os"              # Key storage backend (os, file, test)
  dir: "keys"                # Directory to store keyring files (relative to basedir)
  passphrase_plain: ""       # Plain text passphrase (use only for testing)
  passphrase_env: ""         # Environment variable containing passphrase
  passphrase_file: ""        # Path to file containing passphrase
```

### P2P Configuration
```yaml
p2p:
  port: 4445                 # P2P communication port (do not change)
  data_dir: "data/p2p"       # Directory for P2P data storage
```

### Lumera Network Configuration
```yaml
lumera:
  grpc_addr: "localhost:9090"    # gRPC endpoint of Lumera validator node
  chain_id: "lumera-mainnet-1"   # Lumera blockchain chain identifier
```

### RaptorQ Configuration
```yaml
raptorq:
  files_dir: "raptorq_files"     # Directory to store RaptorQ files
```

**Notes:**
- Relative paths are resolved relative to the base directory (`~/.supernode` by default)
- Absolute paths are used as-is
- The P2P port should not be changed from the default value
