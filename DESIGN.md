``````
# Version and Compatibility Management

## Overview

The Supernode Compatibility System is designed to enable version compatibility and capability matching across the Lumera supernode network. This system provides a foundation for future mesh-based task coordination while maintaining backward compatibility with existing supernodes.

The design introduces a capability advertisement and verification mechanism that allows supernodes to:
- Advertise their version and supported actions
- Query peer capabilities for compatibility checks
- Make informed decisions about task coordination
- Gracefully handle version mismatches

## Architecture

### High-Level Components

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   gRPC Service  │    │  Compatibility   │    │  Configuration  │
│   (External)    │◄──►│     Manager      │◄──►│    Manager      │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

### Component Responsibilities

1. **Configuration Manager**: Handles loading and validation of capability configuration from YAML
2. **Compatibility Manager**: Core logic for version comparison and compatibility checking
3. **gRPC Service Extension**: New RPC methods integrated into existing supernode service

## Components and Interfaces

### Configuration Manager

```go
type ConfigManager interface {
    LoadConfig(path string) (*CapabilityConfig, error)
}

type CapabilityConfig struct {
    Version         string              `yaml:"version"`
    SupportedActions []string           `yaml:"supported_actions"`
    ActionVersions  map[string][]string `yaml:"action_versions"`
    Metadata        map[string]string   `yaml:"metadata,omitempty"`
}
```



### Compatibility Manager

```go
type CompatibilityManager interface {
    CheckCompatibility(local, peer *Capabilities) (*CompatibilityResult, error)
    IsVersionCompatible(localVersion, peerVersion string) (bool, error)
    HasRequiredCapabilities(caps *Capabilities, required []string) bool
}

type CompatibilityResult struct {
    Compatible bool
    Reason     string
    Details    map[string]interface{}
}
```



### gRPC Service Integration

```protobuf
service SupernodeService {
    // Existing methods...
    
    // New capability method
    rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
}

message Capabilities {
    string version = 1;
    repeated string supported_actions = 2;
    map<string, ActionVersions> action_versions = 3;
    map<string, string> metadata = 4;
    int64 timestamp = 5;
}

message ActionVersions {
    repeated string versions = 1;
}
```



## Data Models

### Core Data Structures

```go
type Capabilities struct {
    Version          string              `json:"version"`
    SupportedActions []string            `json:"supported_actions"`
    ActionVersions   map[string][]string `json:"action_versions"`
    Metadata         map[string]string   `json:"metadata,omitempty"`
    Timestamp        time.Time           `json:"tim

type VersionInfo struct {
    Major int `json:"major"`
    Minor int `json:"minor"`
    Patch int `json:"patch"`
}
```

### Configuration Schema

```yaml
# capabilities.yaml
version: "1.2.3"
supported_actions:
  - "cascade"
  - "sense"
  - "storage_challenge"
action_versions:
  cascade: ["1.0.0", "1.1.0", "1.2.0"]
  sense: ["2.0.0", "2.1.0"]
metadata:
  build_info: "go1.24.1"
  features: "raptorq,sqlite"
```
``````


## Lumera Blockchain Integration

### Supernode Parameters Enhancement

Instead of relying solely on local YAML configuration, the system integrates with Lumera's blockchain parameters to maintain network-wide action version compatibility:

```protobuf
// In lumera/proto/lumera/supernode/params.proto
message ActionVersions {
  repeated string versions = 1;
}

message Params {
  // ... existing fields ...
  
  // Action versions define the supported version ranges for each available action
  map<string, ActionVersions> action_versions = 8 [
    (gogoproto.moretags) = "yaml:\"action_versions\""
  ];
}
```

### Benefits of Blockchain Integration

- **Network-wide Coordination**: All supernodes can query the same action version requirements from the blockchain
- **Governance Control**: Action versions can be updated through blockchain governance proposals
- **Fallback Compatibility**: Even if semantic versioning doesn't match, supernodes can still coordinate using available capabilities defined in blockchain parameters
- **Dynamic Updates**: Version requirements can be updated without supernode restarts

### Compatibility Strategy

The system uses a multi-layered compatibility approach:

1. **Semantic Version Matching**: Primary compatibility check using major version matching
2. **Action Version Intersection**: Secondary check using blockchain-defined version ranges
3. **Graceful Degradation**: Fallback to compatible subset of actions when full compatibility isn't available

```go
// Enhanced compatibility checking with blockchain parameters
func (cm *CompatibilityManager) CheckCompatibilityWithBlockchain(
    local, peer *Capabilities, 
    networkActionVersions map[string][]string,
) (*CompatibilityResult, error) {
    // 1. Check semantic version compatibility
    if compatible, _ := cm.IsVersionCompatible(local.Version, peer.Version); compatible {
        return &CompatibilityResult{
            Compatible: true, 
            Reason: "version_compatible",
        }, nil
    }
    
    // 2. Check action version intersection with network parameters
    compatibleActions := []string{}
    for action, networkVersions := range networkActionVersions {
        if cm.hasVersionIntersection(local.ActionVersions[action], networkVersions) &&
           cm.hasVersionIntersection(peer.ActionVersions[action], networkVersions) {
            compatibleActions = append(compatibleActions, action)
        }
    }
    
    if len(compatibleActions) > 0 {
        return &CompatibilityResult{
            Compatible: true,
            Reason: "partial_compatibility",
            Details: map[string]interface{}{
                "compatible_actions": compatibleActions,
            },
        }, nil
    }
    
    return &CompatibilityResult{Compatible: false, Reason: "incompatible"}, nil
}
```

### Usage Example

```yaml
# Local capabilities.yaml (supernode-specific)
version: "1.2.3"
supported_actions:
  - "cascade"
  - "sense"
action_versions:
  cascade: ["1.0.0", "1.1.0", "1.2.0"]
  sense: ["2.0.0", "2.1.0"]
```

```bash
# Network parameters (blockchain-managed)
action_versions:
  cascade: ["1.0.0", "1.1.0", "1.2.0", "1.3.0"]
  sense: ["2.0.0", "2.1.0", "2.2.0"]
  storage_challenge: ["1.0.0"]
```

In this example, even if two supernodes have different semantic versions, they can still coordinate on `cascade` and `sense` actions as long as their action versions intersect with the network-defined ranges.