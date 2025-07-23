# Lumera Supernode

Lumera Supernode is a companion application for Lumera validators who want to provide cascade, sense, and other services to earn rewards.

## gRPC API

The supernode exposes two main gRPC services:

### SupernodeService

Provides system status and monitoring information.

**Service Definition:**
```protobuf
service SupernodeService {
  rpc GetStatus(StatusRequest) returns (StatusResponse);
}
```

**StatusResponse includes:**
- `CPU` - CPU usage and remaining capacity
- `Memory` - Total, used, available memory and usage percentage  
- `ServiceTasks` - Task information for each active service
- `available_services` - List of available service names

### CascadeService

Handles cascade operations for data storage and retrieval.

**Service Definition:**
```protobuf
service CascadeService {
  rpc Register (stream RegisterRequest) returns (stream RegisterResponse);
  rpc Download (DownloadRequest) returns (stream DownloadResponse);
}
```

**Register Operation:**
```protobuf
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
```

**Download Operation:**
```protobuf
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
```

**Event Types:**
- `ACTION_RETRIEVED`, `ACTION_FEE_VERIFIED`, `TOP_SUPERNODE_CHECK_PASSED`
- `METADATA_DECODED`, `DATA_HASH_VERIFIED`, `INPUT_ENCODED`
- `SIGNATURE_VERIFIED`, `RQID_GENERATED`, `RQID_VERIFIED`
- `ARTEFACTS_STORED`, `ACTION_FINALIZED`, `ARTEFACTS_DOWNLOADED`

## CLI Commands

### Core Commands

#### `supernode init`
Initialize a new supernode with interactive setup.

```bash
supernode init                    # Interactive setup
supernode init --force           # Override existing installation  
supernode init -y                # Use defaults, skip prompts
supernode init --keyring-backend test  # Specify keyring backend with -y
```

**Features:**
- Creates `~/.supernode/config.yml`
- Sets up keyring (test, file, or os backend)
- Key recovery from mnemonic or generates new key
- Network configuration (gRPC address, ports, chain ID)

#### `supernode start`
Start the supernode service.

```bash
supernode start                   # Use default config directory
supernode start -d /path/to/dir   # Use custom base directory
```

**Initializes:**
- P2P networking service
- gRPC server (default port 4444)
- Cascade service for data operations
- Connection to Lumera validator node

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

**Output format:**
```
NAME               ADDRESS
----               -------
mykey (selected)   lumera15t2e8gjgmuqtj4jzjqfkf3tf5l8vqw69hmrzmr
backup             lumera1abc...xyz
```

#### `supernode keys add <name>`
Create a new key (generates mnemonic).

#### `supernode keys recover <name>`
Recover key from existing mnemonic phrase.

### Configuration Management

#### `supernode config list`
Display current configuration parameters.

```bash
supernode config list
```

**Shows:**
- Key Name, Address, Supernode Address/Port
- Keyring Backend, Lumera gRPC Address, Chain ID

#### `supernode config update`
Interactive configuration parameter updates.

```bash
supernode config update
```

**Updatable parameters:**
- Supernode IP Address and Port
- Lumera gRPC Address and Chain ID  
- Key Name (with key selection from keyring)
- Keyring Backend (with key migration)

### Global Flags

#### `--basedir, -d`
Specify custom base directory (default: `~/.supernode`).

```bash
supernode start -d /custom/path
supernode config list -d /custom/path
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
