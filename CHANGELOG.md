# Changelog


#### **sn-manager Tool (#114)**
- **Commands**: `init`, `start`, `stop`, `status`, `get`, `ls`, `ls-remote`, `check`, `use`
- **Key Features**:
  - Automated supernode lifecycle management with process supervision
  - GitHub integration for release downloads and automatic updates
  - Automatic Zero Downtime updates
  - Manual Version management with semantic versioning support
  - Configuration management with YAML support


#### **Enhanced Status API (#112)**
- **Detailed metrics integration** - Added comprehensive system metrics to status responses
- **New Response Fields**:
  - Version information and build details
  - CPU, memory, and disk usage metrics with percentages
  - Network statistics and active connection counts
  - Cascade service performance metrics and task enumeration
  - System uptime and resource utilization tracking
  - SuperNode ranking and peer information


#### ** sncli **

- **gRPC reflection support** for dynamic service/method introspection
- **Security Features**:
  - Secure gRPC connections with Lumera keyring authentication
  - TLS certificate validation and encrypted communications
  - Command-line config overrides and environment variable support

### Improvements & Optimizations

#### **Context Handling & Server Optimization (#119)**
- **Background task improvements** - Implemented detached contexts for SDK background operations
- **gRPC server optimization** - Enhanced settings for improved file size handling and performance
- **Error handling enhancements** in cascade action servers with better context propagation
- **Scope**: 8 files modified across SDK task management and supernode server components

#### **Configuration Refactoring (#122)**
- **Network interface consistency** - Unified network configuration across all components


#### **CI/CD Workflow Optimization (#120)**
- **GitHub Actions streamlining** - Reduced workflow complexity 
- **Build process improvements** for both supernode and sn-manager components
- **Enhanced pipeline** with better job organization and dependency management
- **Performance**: Faster build times and improved resource utilization

###  Bug Fixes

#### **P2P Connection Reliability (#117)**
- **Fixed connection deadlines** in Kademlia DHT operations preventing timeouts
- **Credential separation** eliminate a race condition by using difference credentials manager

#### **Logging System Improvements (#115)**
- **Disabled stacktrace by default** for all log levels to reduce noise in production




### 📋 API Changes & Compatibility
- **New RPC Endpoints**: `ListServices` for service discovery and introspection
- **Enhanced Responses**: `StatusResponse` includes hardware, network, and performance metrics

