# SN-Manager

SuperNode Process Manager with Automatic Updates

## Installation

Download and install sn-manager:
```bash
# Download and extract
curl -L https://github.com/LumeraProtocol/supernode/releases/latest/download/supernode-linux-amd64.tar.gz | tar -xz

# Install sn-manager only (supernode will be managed automatically)
chmod +x sn-manager
sudo mv sn-manager /usr/local/bin/

# Verify
sn-manager version
```

Note: SuperNode binary will be automatically downloaded and managed by sn-manager during initialization.

## Systemd Service Setup

**Replace `<YOUR_USER>` with your Linux username:**

```bash
sudo tee /etc/systemd/system/sn-manager.service <<EOF
[Unit]
Description=Lumera SuperNode Manager
After=network-online.target

[Service]
User=<YOUR_USER>
ExecStart=/usr/local/bin/sn-manager start
Restart=on-failure
RestartSec=10
LimitNOFILE=65536
Environment="HOME=/home/<YOUR_USER>"
WorkingDirectory=/home/<YOUR_USER>

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now sn-manager
journalctl -u sn-manager -f
```

## Initialization

### Interactive Mode
```bash
sn-manager init
```

### Non-Interactive Mode

**Basic setup:**
```bash
sn-manager init -y
```

**Full example with all flags:**
```bash
export SUPERNODE_PASSPHRASE="your-secure-passphrase"

sn-manager init -y \
  --auto-upgrade \
  --check-interval 3600 \
  --keyring-backend file \
  --keyring-passphrase-env SUPERNODE_PASSPHRASE \
  --key-name myvalidator \
  --supernode-addr 0.0.0.0 \
  --supernode-port 4444 \
  --lumera-grpc https://grpc.lumera.io:443 \
  --chain-id lumera-testnet-2
```

**With key recovery:**
```bash
sn-manager init -y \
  --keyring-backend file \
  --keyring-passphrase "your-secure-passphrase" \
  --key-name myvalidator \
  --recover \
  --mnemonic "word1 word2 ... word24" \
  --supernode-addr 0.0.0.0 \
  --lumera-grpc https://grpc.lumera.io:443 \
  --chain-id lumera-testnet-2
```

### Flags

**SN-Manager flags:**
- `--force` - Override existing configuration
- `--auto-upgrade` - Enable automatic updates
- `--check-interval` - Update check interval in seconds

**SuperNode flags (passed through):**
- `-y` - Skip prompts
- `--keyring-backend` - Backend type (os/file/test)
- `--keyring-passphrase` - Plain text passphrase
- `--keyring-passphrase-env` - Environment variable name
- `--keyring-passphrase-file` - File path
- `--key-name` - Key identifier
- `--recover` - Recover from mnemonic
- `--mnemonic` - Recovery phrase
- `--supernode-addr` - Bind address
- `--supernode-port` - Service port
- `--lumera-grpc` - gRPC endpoint
- `--chain-id` - Chain identifier

## Commands

- `init` - Initialize sn-manager and SuperNode
- `start` - Start SuperNode
- `stop` - Stop SuperNode
- `status` - Show status
- `version` - Show version
- `get <version>` - Download version
- `use <version>` - Switch version
- `ls` - List installed versions
- `ls-remote` - List available versions
- `check` - Check for updates

## Configuration

### SN-Manager (`~/.sn-manager/config.yml`)
```yaml
updates:
  current_version: "v1.7.4"
  auto_upgrade: true
  check_interval: 3600
```

**Reset:**
```bash
sudo systemctl stop sn-manager
rm -rf ~/.sn-manager/ ~/.supernode/
sn-manager init
```

