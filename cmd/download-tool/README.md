# Supernode Download Tool

A standalone command-line tool for downloading files from the Lumera supernode network using cascade action IDs. Uses SDK auto-discovery to find and try available supernodes automatically. Features automatic configuration detection and user-friendly operation.

## Features

- 🔍 **Auto-Detection**: Automatically reads configuration from `~/.supernode/config.yaml`
- 🔑 **Smart Key Management**: Auto-detects keys and addresses from keyring
- 📋 **Key Listing**: View all available keys with `-list-keys`
- 🛡️ **Secure Downloads**: Uses SDK action client with ALTS authentication
- 🔧 **Manual Override**: All settings can be manually configured
- ✅ **Backward Compatible**: Works with existing workflows

## Building

```bash
go build -o download-tool .
```

## Quick Start

### With Auto-Detection (Recommended)

If you have supernode initialized:

```bash
# Simple download (config auto-detected)
./download-tool -action abc123

# List available keys
./download-tool -list-keys
```

### Manual Configuration

```bash
# Full manual configuration
./download-tool -action abc123 -key-name mykey -local-addr lumera1abc...
```

## Usage

### Required Parameters

- `-action <id>` - Action ID to download

### Auto-Detected Parameters

When `~/.supernode/config.yaml` exists, these are automatically detected:
- Key name and local cosmos address (from config `identity` field)
- Keyring backend and directory
- Lumera gRPC endpoint and chain ID

### Optional Parameters

- `-out <path>` - Output file path (default: `<action_id>.dat`)
- `-signature <sig>` - Signature for verification
- `-timeout <duration>` - Download timeout (default: 5m)
- `-auto-detect <bool>` - Enable auto-detection (default: true)

### Manual Override Parameters

- `-key-name <key>` - Override auto-detected key name
- `-local-addr <addr>` - Override auto-detected local address
- `-keyring-backend <backend>` - Override keyring backend (os, file, test)
- `-keyring-dir <dir>` - Override keyring directory
- `-lumera-grpc <addr>` - Override Lumera gRPC endpoint
- `-chain-id <id>` - Override chain ID

## Examples

```bash
# Auto-detection examples
./download-tool -action abc123
./download-tool -action abc123 -out ./downloads/file.dat
./download-tool -action abc123 -chain-id mainnet

# Manual configuration examples  
./download-tool -action abc123 -key-name mykey -local-addr lumera1abc...
./download-tool -auto-detect=false -action abc123 -key-name mykey -local-addr lumera1abc...

# Utility commands
./download-tool -list-keys
./download-tool -help
```

## Prerequisites

### With Auto-Detection
- Supernode initialized (`supernode init` completed)
- Valid key in supernode keyring

### Manual Configuration
- Access to a keyring with valid keys
- Knowledge of your local cosmos address
- Access to Lumera blockchain endpoint

## Output

The tool provides informative output during operation:

```
📋 Auto-detected supernode config: /home/user/.supernode/config.yaml
🔐 Using keyring: os
🔑 Using key: mykey (auto-detected)
👤 Local address: lumera1abc... (from config identity)
📥 Downloading action ID: abc123
💾 Output file: abc123.dat
🚀 Starting secure download using SDK action client...
📋 Download task created: task_abc123_download
📊 Download completed: 1.2 MB (1258291 bytes)
✅ Download completed successfully: abc123.dat
```

## Error Handling

The tool provides clear error messages and suggestions:

- Missing parameters: Guidance on required flags or config setup
- Config issues: Instructions to check supernode initialization
- Key problems: Suggestion to use `-list-keys` to see available options
- Network errors: Clear indication of connection or authentication issues

## Security

- Uses ALTS (Application Layer Transport Security) for encrypted connections
- Integrates with system keyring for secure key management
- Follows same security patterns as supernode itself
- All authentication handled through Lumera blockchain validation

## Integration

This tool can be:
- Used interactively from command line
- Integrated into scripts and automation
- Called from other applications
- Used in CI/CD pipelines
- Deployed alongside supernode services