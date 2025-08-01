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

message StatusRequest {}

message StatusResponse {
  message CPU {
    string usage = 1;
    string remaining = 2;
  }

  message Memory {
    uint64 total = 1;
    uint64 used = 2;
    uint64 available = 3;
    double used_perc = 4;
  }

  message ServiceTasks {
    string service_name = 1;
    repeated string task_ids = 2;
    int32 task_count = 3;
  }

  CPU cpu = 1;
  Memory memory = 2;
  repeated ServiceTasks services = 3;
  repeated string available_services = 4;
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
  ARTEFACTS_STORED = 10;
  ACTION_FINALIZED = 11;
  ARTEFACTS_DOWNLOADED = 12;
}
```

## HTTP Gateway

The supernode provides an HTTP gateway that exposes the gRPC services via REST API. The gateway runs on a separate port (default: 8002) and provides:

### Endpoints

#### GET /status
Returns the current supernode status including CPU, memory usage and active services.

```bash
curl http://localhost:8002/status
```

Response:
```json
{
  "cpu": {
    "usage": "15.2%",
    "remaining": "84.8%"
  },
  "memory": {
    "total": "16777216000",
    "used": "8388608000",
    "available": "8388608000",
    "usedPerc": 50.0
  },
  "services": [
    {
      "serviceName": "cascade",
      "taskIds": ["task1", "task2"],
      "taskCount": 2
    }
  ],
  "availableServices": ["cascade", "sense"]
}
```

#### GET /services
Returns the list of available services on the supernode.

```bash
curl http://localhost:8002/services
```

Response:
```json
{
  "services": ["cascade", "sense"]
}
```

The gateway automatically translates between HTTP/JSON and gRPC/protobuf formats, making it easy to integrate with web applications and monitoring tools.

### API Documentation

The gateway provides interactive API documentation via Swagger UI:

- **Swagger UI**: http://localhost:8002/swagger-ui/
- **OpenAPI Spec**: http://localhost:8002/swagger.json

The Swagger UI provides an interactive interface to explore and test all available API endpoints.

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
  --gateway-port 8002 \
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
  --gateway-port 8002 \
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
  --gateway-port 8002 \
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
- `--gateway-port` - Port for the HTTP gateway to listen on
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
  ip_address: "0.0.0.0"               # IP address to bind the service
  port: 4444                          # Port for the supernode service
  gateway_port: 8002                   # Port for the HTTP gateway service
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
  listen_address: "0.0.0.0"  # IP address for P2P networking
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
