# SN-Manager

SuperNode Process Manager with Automatic Updates

## Table of Contents

- [Installation](#installation)
- [Systemd Service Setup](#systemd-service-setup)
- [Ensure PATH points to user install](#ensure-path-points-to-user-install-required-for-self-update)
- [Initialization](#initialization)
- [Commands](#commands)
- [Version Update Scenarios](#version-update-scenarios)
- [Start/Stop Behavior](#startstop-behavior)
- [Migration for Existing sn-manager Users](#migration-for-existing-sn-manager-users)
- [Troubleshooting](#troubleshooting)
  - [Fix non-writable install](#fix-non-writable-install)
- [Configuration](#configuration)
- [Notes](#notes)

## Installation

Download and install sn-manager:
Note: Supported on Linux x86_64 (amd64). Other architectures are not yet supported.
```bash

# Download and extract
# Always fetch the latest stable release asset
curl -L https://github.com/LumeraProtocol/supernode/releases/latest/download/supernode-linux-amd64.tar.gz | tar -xz

# Install sn-manager to a user-writable location (enables self-update)
install -D -m 0755 sn-manager "$HOME/.sn-manager/bin/sn-manager"

# Verify
"$HOME/.sn-manager/bin/sn-manager" version

# Optional: add to PATH for convenience
echo 'export PATH="$HOME/.sn-manager/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc && hash -r

# Confirm the path used resolves to our install first
command -v -a sn-manager
readlink -f "$(command -v sn-manager)"
```

Note: SuperNode binary will be automatically downloaded and managed by sn-manager during initialization. Installing sn-manager under your home directory allows it to auto-update itself.

## Systemd Service Setup

**Replace `<YOUR_USER>` with your Linux username:**

```bash
sudo tee /etc/systemd/system/sn-manager.service <<EOF
[Unit]
Description=Lumera SuperNode Manager
After=network-online.target

[Service]
User=<YOUR_USER>
ExecStart=/home/<YOUR_USER>/.sn-manager/bin/sn-manager start
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

Stop/start later or restart after changes:
```bash
sudo systemctl stop sn-manager
sudo systemctl start sn-manager

# or
sudo systemctl restart sn-manager
journalctl -u sn-manager -f
```

### Ensure PATH points to user install (Required for self-update)

To ensure self-update works and avoid conflicts, make sure your shell resolves to the user-writable install:

```bash
# List all sn-manager binaries found on PATH (our path should be first)
command -v -a sn-manager

# Remove any global copy (e.g., /usr/local/bin/sn-manager)
sudo rm -f /usr/local/bin/sn-manager || true

# Clear shell command cache and verify again
hash -r
command -v -a sn-manager
readlink -f "$(command -v sn-manager)"
```

The systemd unit uses an absolute `ExecStart` pointing to your home directory, so the service will always run the intended binary regardless of PATH.

Note: Auto-upgrade requires the sn-manager binary directory to be writable by the service user. If you encounter an error like:

```
auto-upgrade is enabled but sn-manager binary directory is not writable (...)
```

follow the steps in [Fix non-writable install](#fix-non-writable-install).

## Initialization

### Interactive Mode
```bash
sn-manager init
```

> !!! If SuperNode was already initialized before, use `sn-manager init` without parameters OR with SN-Manager-only flags (see below)!!!

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

Disable auto-upgrade non-interactively:
```bash
sn-manager init -y --auto-upgrade false
```

### Flags

Note: Unrecognized flags to `sn-manager init` are passed through to the underlying `supernode init`.

**SN-Manager flags:**
- `--force` - Override existing configuration
- `--auto-upgrade` or `--auto-upgrade true|false` - Enable/disable automatic updates (default: enabled)

Auto-update checks run every 10 minutes when enabled.

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
- `start` - Start sn-manager and SuperNode
- `stop` - Stop sn-manager and SuperNode
- `status` - Show status
- `version` - Show version
- `get <version>` - Download version
- `use <version>` - Switch version
- `ls` - List installed versions
- `ls-remote` - List available stable versions
- `check` - Check for updates
- `supernode start` - Start SuperNode (requires sn-manager running)
- `supernode stop` - Stop SuperNode and prevent auto-restart
- `supernode status` - Show SuperNode status

## Version Update Scenarios

The auto-updater follows stable-only, same-major update rules and coordinates updates around the gateway state to minimize disruption while avoiding being stuck. Summary:

| Current | Available | Auto-Upgrade Enabled | Auto Updates? | Manual Option |
|---|---|---|---|---|
| v1.7.1 | v1.7.4 (stable) | Yes | ✅ | — |
| v1.7.1-beta | v1.7.1 (stable) | Yes | ✅ | — |
| v1.7.4 | v1.8.0 (stable) | Yes | ✅ | — |
| v1.7.4 | v1.8.0-rc1 (pre-release) | Yes | ❌ | `sn-manager get v1.8.0-rc1 && sn-manager use v1.8.0-rc1` |
| v1.7.4 | v1.7.4 (stable) | Yes | ❌ | — |
| v1.7.5 | v1.7.4 (stable) | Yes | ❌ | — |
| Any | Any | No | ❌ | `sn-manager get [version] && sn-manager use [version]` |
| Any | Any | Yes, but gateway busy | ⏳ Deferred (max 1 hour), then ✅ | Manual allowed |

Mechanics and notes:
- Stable-only: auto-updater targets latest stable GitHub release (non-draft, non-prerelease).
- Same-major only: SuperNode and sn-manager auto-update only when the latest is the same major version (the number before the first dot). Example: 1.7 → 1.8 = allowed; 1.x → 2.0 = manual.
- Gateway idle: updates are applied when the gateway reports no running tasks.
- Gateway busy: if tasks are running, updates are deferred for up to 1 hour for the target version; after that hard window, the update proceeds to avoid being stuck.
- Gateway unresponsive: if an update is available, it proceeds immediately to break the deadlock; if no update is available, a clean SuperNode restart is requested via marker.
- Combined tarball: when updating, sn-manager downloads a single tarball once, then updates itself first (if eligible), then installs/activates the new SuperNode version.
- Config is updated to reflect the new `updates.current_version` after a successful SuperNode update.
- Manual installs: you can always override with `sn-manager get <version>` and `sn-manager use <version>`; pre-releases are supported manually.

### Update Timing

- Checks run every 10 minutes when auto-upgrade is enabled.
- On every `sn-manager start`, the updater runs an immediate check and bypasses the gateway check once so that initial updates can be applied even if the gateway is not yet available.

## Start/Stop Behavior

sn-manager start and supernode start clear the stop marker; supernode stop sets it. How the manager and SuperNode processes behave for each command, plus systemd nuances:

| Action | Manager | SuperNode | Marker | systemd (unit uses `Restart=on-failure`) |
|---|---|---|---|---|
| `sn-manager start` | Starts manager ✅ | Starts if no stop marker ✅ | Clears `.stop_requested` if present | Start via `systemctl start sn-manager` when running under systemd |
| `sn-manager stop` | Stops manager ✅ | Stops (graceful, then forced if needed) ✅ | — | Will NOT be restarted by systemd (clean exit) ❌ |
| `sn-manager status` | Reads PID | Reports running/not and versions | — | — |
| `sn-manager supernode start` | Stays running | Starts SuperNode ✅ | Removes `.stop_requested` | — |
| `sn-manager supernode stop` | Stays running | Stops SuperNode ✅ | Writes `.stop_requested` | — |
| SuperNode crash | Stays running | Auto-restarts after backoff ✅ | Skipped if `.stop_requested` present ❌ | — |
| Manager crash | Exits abnormally | — | — | systemd restarts manager ✅ |

Notes:
- Clean exit vs. systemd: If systemd started sn-manager and you run `sn-manager stop`, the manager exits cleanly. With `Restart=on-failure`, systemd does not restart it. Use `systemctl start sn-manager` (or `systemctl restart sn-manager`) to run it again. If you want automatic restarts after clean exits, change the unit to `Restart=always` (not generally recommended as it fights the `stop` intent).
- Stop marker: `.stop_requested` prevents automatic SuperNode restarts by the manager until cleared. `sn-manager supernode start` clears it; `sn-manager start` also clears it on launch.
- PID files: Manager writes `~/.sn-manager/sn-manager.pid`; SuperNode writes `~/.sn-manager/supernode.pid`. Stale PID files are detected and cleaned up.

## Migration for Existing sn-manager Users

If you already run sn-manager, you can align with this guide without re-initializing.

1) Check your current install
- Show paths: `command -v -a sn-manager` and `readlink -f "$(command -v sn-manager)"`.
- Required for self-update: install at `~/.sn-manager/bin/sn-manager` (must be user-writable).
- If you currently use `/usr/local/bin/sn-manager`, self-update will not work reliably. Switch to the user path and remove the global copy:
  `sudo rm -f /usr/local/bin/sn-manager && hash -r`

2) Reinstall to user path (required for self-update)
```bash
curl -L https://github.com/LumeraProtocol/supernode/releases/latest/download/supernode-linux-amd64.tar.gz | tar -xz
install -D -m 0755 sn-manager "$HOME/.sn-manager/bin/sn-manager"
echo 'export PATH="$HOME/.sn-manager/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc && hash -r
sn-manager version
```

3) Keep existing data
- No changes to `~/.supernode` or `~/.sn-manager` are required.
- Do not re-run `supernode init`; your keys and config remain intact.

4) Update or create the systemd unit
- Use the unit from this README. Ensure `ExecStart` points to the intended binary path and set `Environment=HOME=...` and `WorkingDirectory=...` for your user.
- With `Restart=on-failure`, `sn-manager stop` will cleanly exit and systemd will not restart it; start again with `sudo systemctl start sn-manager`.

Update these lines exactly in `/etc/systemd/system/sn-manager.service` (replace `<YOUR_USER>`):
```
[Service]
User=<YOUR_USER>
ExecStart=/home/<YOUR_USER>/.sn-manager/bin/sn-manager start
Environment="HOME=/home/<YOUR_USER>"
WorkingDirectory=/home/<YOUR_USER>
Restart=on-failure
RestartSec=10
LimitNOFILE=65536
```

If your unit currently has `ExecStart=/usr/local/bin/sn-manager start`, change it to the exact `ExecStart` line above.

After editing, reload and restart:
```
sudo systemctl daemon-reload
sudo systemctl restart sn-manager
```

5) Verify and adopt
- Manager status: `sn-manager status`
- Check updates: `sn-manager check`


## Troubleshooting

### Fix non-writable install

Symptom:

```
auto-upgrade is enabled but sn-manager binary directory is not writable (...)
```

Cause: `sn-manager` is installed in a root-owned directory such as `/usr/local/bin`, so the auto-updater cannot write `sn-manager.new` during self-update.

Fix:

1. Reinstall `sn-manager` to a user-writable path:
   ```bash
   curl -L https://github.com/LumeraProtocol/supernode/releases/latest/download/supernode-linux-amd64.tar.gz | tar -xz
   install -D -m 0755 sn-manager "$HOME/.sn-manager/bin/sn-manager"
   echo 'export PATH="$HOME/.sn-manager/bin:$PATH"' >> ~/.bashrc
   source ~/.bashrc && hash -r
   sn-manager version
   ```
2. Update the systemd unit to point to the user install and set HOME/workdir:
   ```ini
   [Service]
   User=<YOUR_USER>
   ExecStart=/home/<YOUR_USER>/.sn-manager/bin/sn-manager start
   Environment="HOME=/home/<YOUR_USER>"
   WorkingDirectory=/home/<YOUR_USER>
   Restart=on-failure
   RestartSec=10
   ```
3. Remove the global copy so PATH doesn’t pick it:
   ```bash
   sudo rm -f /usr/local/bin/sn-manager && hash -r
   ```
4. Restart the service:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart sn-manager
   ```


## Configuration

### SN-Manager (`~/.sn-manager/config.yml`)
```yaml
updates:
  current_version: "v1.7.4"
  auto_upgrade: true
```

**Reset:**

Reset managed data while keeping the installed sn-manager binary:
```bash
sudo systemctl stop sn-manager
rm -rf ~/.supernode/
rm -rf ~/.sn-manager/binaries ~/.sn-manager/downloads ~/.sn-manager/current ~/.sn-manager/config.yml
sn-manager init
```

Full reset (also removes the sn-manager binary; you will need to reinstall it):
```bash
sudo systemctl stop sn-manager
rm -rf ~/.sn-manager/ ~/.supernode/
# Reinstall sn-manager as shown in Installation, then:
sn-manager init
```

## Notes

- By default, `sn-manager start` starts both the manager and SuperNode. You can later control SuperNode independently with `sn-manager supernode start|stop|status`.
- Auto-updates use the latest stable release and apply within the same major version. A single release bundle is downloaded and used to update both sn-manager and SuperNode.
