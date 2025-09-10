# Multi-Cluster Kubernetes MCP Server Implementation Proposal

## Executive Summary

This proposal outlines the implementation of multi-cluster support for the Kubernetes MCP Server, specifically designed to work with Rancher-managed clusters and the NSK tool. The solution enables AI agents to interact with multiple Kubernetes clusters by reading kubeconfig files from a directory and providing cluster switching capabilities.

## Current State Analysis

### Existing Architecture
- **Single Cluster Focus**: Current implementation uses `pkg/config/StaticConfig.KubeConfig` string field
- **Configuration Loading**: `pkg/kubernetes/configuration.go` handles single kubeconfig via `clientcmd.NewDefaultPathOptions()`
- **MCP Tools**: All tools operate against a single cluster context defined at startup
- **Tool Pattern**: Each tool is defined in separate files (e.g., `pkg/mcp/pods.go`, `pkg/mcp/resources.go`)

### Key Integration Points
1. **Configuration**: `pkg/config/config.go` - StaticConfig struct
2. **Kubernetes Manager**: `pkg/kubernetes/configuration.go` - resolveKubernetesConfigurations()
3. **MCP Server**: `pkg/mcp/mcp.go` - Server struct and tool registration
4. **Command Line**: `pkg/kubernetes-mcp-server/cmd/root.go` - CLI flags

## Proposed Architecture

### 1. Multi-Cluster Configuration Management

#### New Configuration Fields
```go
// pkg/config/config.go
type StaticConfig struct {
    // Existing fields...
    KubeConfig string `toml:"kubeconfig,omitempty"`
    
    // Enhanced multi-cluster with NSK integration
    KubeConfigDir     string   `toml:"kubeconfig_dir,omitempty"`
    DefaultCluster    string   `toml:"default_cluster,omitempty"`
    ClusterAliases    map[string]string `toml:"cluster_aliases,omitempty"`
    AutoDiscovery     bool     `toml:"auto_discovery,omitempty"`
    
    // NSK Integration Configuration
    NSKIntegration    *NSKConfig `toml:"nsk,omitempty"`
}

type NSKConfig struct {
    // Core Rancher Environment
    RancherURL        string `toml:"rancher_url,omitempty"`
    RancherToken      string `toml:"rancher_token,omitempty"`
    RancherInsecure   bool   `toml:"rancher_insecure,omitempty"`
    Profile           string `toml:"profile,omitempty"`
    ConfigDir         string `toml:"config_dir,omitempty"`
    
    // Auto-refresh settings
    AutoRefresh       bool   `toml:"auto_refresh,omitempty"`
    RefreshInterval   string `toml:"refresh_interval,omitempty"` // e.g., "1h", "30m"
    
    // NSK command path
    NSKPath           string `toml:"nsk_path,omitempty"` // default: "nsk"
    
    // Cluster filtering
    ClusterPattern    string   `toml:"cluster_pattern,omitempty"` // regex pattern for cluster names
    ExcludeClusters   []string `toml:"exclude_clusters,omitempty"`
}
```

#### Cluster Discovery Service
```go
// pkg/kubernetes/cluster_manager.go
type ClusterManager struct {
    clusters        map[string]*ClusterConfig
    activeCluster   string
    configDir       string
    staticConfig    *config.StaticConfig
    mutex          sync.RWMutex
}

type ClusterConfig struct {
    Name           string
    KubeConfigPath string
    Manager        *Manager
    LastRefresh    time.Time
    IsActive       bool
}

func NewClusterManager(configDir string, staticConfig *config.StaticConfig) *ClusterManager
func (cm *ClusterManager) DiscoverClusters() error
func (cm *ClusterManager) GetCluster(name string) (*ClusterConfig, error)
func (cm *ClusterManager) SetActiveCluster(name string) error
func (cm *ClusterManager) ListClusters() []ClusterConfig
func (cm *ClusterManager) RefreshCluster(name string) error
```

### 2. New MCP Tools for Cluster Management

#### Cluster Operations Tools
```go
// pkg/mcp/clusters.go
func (s *Server) initClusters() []server.ServerTool {
    return []server.ServerTool{
        {Tool: mcp.NewTool("clusters_list",
            mcp.WithDescription("List all available Kubernetes clusters from kubeconfig directory"),
            mcp.WithBoolean("show_details", mcp.Description("Include cluster details like server URL and namespace")),
            mcp.WithTitleAnnotation("Clusters: List"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.clustersListe},
        
        {Tool: mcp.NewTool("clusters_switch",
            mcp.WithDescription("Switch active cluster context"),
            mcp.WithString("cluster", mcp.Description("Cluster name to switch to"), mcp.Required()),
            mcp.WithTitleAnnotation("Clusters: Switch Context"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.clustersSwitch},
        
        {Tool: mcp.NewTool("clusters_status",
            mcp.WithDescription("Get status of all clusters including connectivity"),
            mcp.WithTitleAnnotation("Clusters: Status"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.clustersStatus},
        
        {Tool: mcp.NewTool("clusters_exec_all",
            mcp.WithDescription("Execute a command across all clusters"),
            mcp.WithString("operation", mcp.Description("Operation to execute"), mcp.Required()),
            mcp.WithObject("parameters", mcp.Description("Parameters for the operation")),
            mcp.WithBoolean("fail_fast", mcp.Description("Stop on first failure")),
            mcp.WithTitleAnnotation("Clusters: Execute on All"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.clustersExecAll},
    }
}
```

#### NSK Integration Tools
```go
// pkg/mcp/nsk.go
func (s *Server) initNSKTools() []server.ServerTool {
    if s.nsk == nil {
        return nil // NSK integration not configured
    }
    
    return []server.ServerTool{
        {Tool: mcp.NewTool("nsk_refresh",
            mcp.WithDescription("Manually refresh kubeconfig files from Rancher via NSK"),
            mcp.WithBoolean("force", mcp.Description("Force refresh even if recent refresh occurred")),
            mcp.WithTitleAnnotation("NSK: Refresh Kubeconfigs"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.nskRefresh},
        
        {Tool: mcp.NewTool("nsk_status",
            mcp.WithDescription("Get NSK integration status and last refresh time"),
            mcp.WithTitleAnnotation("NSK: Integration Status"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.nskStatus},
        
        {Tool: mcp.NewTool("nsk_clusters_discover",
            mcp.WithDescription("Discover new clusters from Rancher and update kubeconfigs"),
            mcp.WithString("pattern", mcp.Description("Cluster name pattern to filter")),
            mcp.WithTitleAnnotation("NSK: Discover Clusters"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.nskClustersDiscover},
    }
}
```

#### Enhanced Existing Tools
```go
// All existing tools get optional cluster parameter
{Tool: mcp.NewTool("pods_list",
    mcp.WithDescription("List all Kubernetes pods in the current cluster from all namespaces"),
    mcp.WithString("cluster", mcp.Description("Cluster name (optional, uses active cluster if not specified)")),
    mcp.WithString("labelSelector", mcp.Description("Optional Kubernetes label selector")),
    // ...existing parameters
), Handler: s.podsListInAllNamespaces},
```

### 3. Cluster Context Switching Mechanism

#### Context Management
```go
// pkg/kubernetes/context_manager.go
type ContextManager struct {
    clusterManager *ClusterManager
    currentContext string
    sessionContexts map[string]string // session-specific cluster contexts
}

func (cm *ContextManager) SwitchContext(clusterName string) error
func (cm *ContextManager) GetCurrentContext() string
func (cm *ContextManager) WithClusterContext(clusterName string, fn func(*Manager) error) error
```

#### Session-Based Context (for HTTP mode)
```go
// pkg/mcp/mcp.go - Enhanced Server struct
type Server struct {
    configuration    *Configuration
    k               *kubernetes.Kubernetes
    clusterManager  *kubernetes.ClusterManager  // New
    contextManager  *kubernetes.ContextManager  // New
    server          server.ServerInterface
}
```

### 4. Configuration Loading Strategy

#### Directory Scanning
```go
// pkg/kubernetes/discovery.go
func (cm *ClusterManager) scanKubeConfigDirectory(dir string) (map[string]string, error) {
    clusters := make(map[string]string)
    
    files, err := os.ReadDir(dir)
    if err != nil {
        return nil, err
    }
    
    for _, file := range files {
        if strings.HasSuffix(file.Name(), ".yaml") || strings.HasSuffix(file.Name(), ".yml") {
            clusterName := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
            clusters[clusterName] = filepath.Join(dir, file.Name())
        }
    }
    
    return clusters, nil
}
```

#### NSK Integration Helper
```go
// pkg/kubernetes/nsk_integration.go
type NSKIntegration struct {
    nsk_path string
    profiles map[string]string
}

func (nsk *NSKIntegration) RefreshKubeConfigs(clusterPattern string) error
func (nsk *NSKIntegration) GetClusterList() ([]string, error)
```

## Implementation Phases

### Phase 1: Core Infrastructure (Week 1-2)
1. **Extend Configuration Schema**
   - Add multi-cluster fields to `StaticConfig`
   - Update TOML parsing and validation
   - Add CLI flags for kubeconfig directory

2. **Implement ClusterManager**
   - Create cluster discovery and management
   - Implement kubeconfig directory scanning
   - Basic cluster switching functionality

3. **Update Core Kubernetes Manager**
   - Modify `resolveKubernetesConfigurations()` to support cluster manager
   - Implement per-cluster Manager instances

### Phase 2: MCP Tools Implementation (Week 3)
1. **Create Cluster Management Tools**
   - Implement `clusters_list`, `clusters_switch`, `clusters_status`
   - Add comprehensive error handling and validation

2. **Enhance Existing Tools**
   - Add optional cluster parameter to all existing tools
   - Implement cluster-aware operations
   - Maintain backward compatibility

### Phase 3: Advanced Features (Week 4)
1. **Cross-Cluster Operations**
   - Implement `clusters_exec_all` functionality
   - Add aggregation and reporting capabilities
   - Implement fail-fast and error handling strategies

2. **NSK Integration**
   - Create NSK helper utilities
   - Implement automatic kubeconfig refresh
   - Add cluster discovery from NSK commands

### Phase 4: Testing and Polish (Week 5)
1. **Comprehensive Testing**
   - Unit tests for all new components
   - Integration tests with multiple clusters
   - Performance testing with large cluster sets

2. **Documentation and Examples**
   - Update README with multi-cluster examples
   - Create configuration templates
   - Add troubleshooting guides

## Configuration Examples

### Environment-Specific Multi-Cluster Setup

#### Production Environment
```toml
# config-production.toml
[nsk]
rancher_url = "https://rancher.netskope.io"
rancher_token = "${PROD_RANCHER_TOKEN}"
profile = "prod"
config_dir = "/etc/kubernetes/clusters/prod"
auto_refresh = true
refresh_interval = "1h"
cluster_pattern = "^c1-.*"

kubeconfig_dir = "/etc/kubernetes/clusters/prod"
default_cluster = "c1-sv5"
auto_discovery = true

[cluster_aliases]
"production" = "c1-sv5"
"prod-west" = "c1-lax1"
"prod-east" = "c1-dfw1"
```

#### Staging Environment
```toml
# config-staging.toml
[nsk]
rancher_url = "https://rancher.prime.iad0.netskope.com"
rancher_token = "${STAGING_RANCHER_TOKEN}"
profile = "staging"
config_dir = "/etc/kubernetes/clusters/staging"
auto_refresh = true
refresh_interval = "30m"

kubeconfig_dir = "/etc/kubernetes/clusters/staging"
default_cluster = "iad0-sandbox"
auto_discovery = true
```

#### Development Environment
```toml
# config-development.toml
[nsk]
rancher_url = "https://rancher-dev.netskope.io"
rancher_token = "${DEV_RANCHER_TOKEN}"
profile = "dev"
config_dir = "/etc/kubernetes/clusters/dev"
auto_refresh = true
refresh_interval = "15m"
exclude_clusters = ["legacy-cluster", "deprecated-test"]

kubeconfig_dir = "/etc/kubernetes/clusters/dev"
default_cluster = "dev-cluster"
auto_discovery = true
```

### Command Line Usage
```bash
# Production MCP server
kubernetes-mcp-server --config config-production.toml --port 8080

# Development with NSK integration override
kubernetes-mcp-server \
  --nsk-rancher-url https://rancher-dev.netskope.io \
  --nsk-profile dev \
  --nsk-config-dir ~/.nsk-dev \
  --kubeconfig-dir ~/.nsk-dev \
  --nsk-auto-refresh \
  --port 8081

# Staging with environment variables
export RANCHER_URL="https://rancher.prime.iad0.netskope.com"
export RANCHER_TOKEN="token-xxx"
export NSK_PROFILE="staging"
export NSK_CONFDIR="/tmp/staging-clusters"

kubernetes-mcp-server \
  --kubeconfig-dir /tmp/staging-clusters \
  --nsk-auto-refresh \
  --port 8082
```

## Expected Tool Usage Patterns

### AI Agent Interactions
```json
// List all clusters
{"method": "tools/call", "params": {"name": "clusters_list"}}

// Switch to production cluster
{"method": "tools/call", "params": {"name": "clusters_switch", "arguments": {"cluster": "production"}}}

// List pods in specific cluster
{"method": "tools/call", "params": {"name": "pods_list", "arguments": {"cluster": "c1-sv5"}}}

// Execute across all clusters
{"method": "tools/call", "params": {"name": "clusters_exec_all", "arguments": {"operation": "pods_list", "parameters": {"labelSelector": "app=nginx"}}}}

// NSK integration operations
{"method": "tools/call", "params": {"name": "nsk_refresh", "arguments": {"force": true}}}
{"method": "tools/call", "params": {"name": "nsk_status"}}
{"method": "tools/call", "params": {"name": "nsk_clusters_discover", "arguments": {"pattern": "^c1-.*"}}}
```

## Benefits

### For AI Agents
- **Multi-Environment Operations**: Query and operate across development, staging, production
- **Cluster Comparison**: Compare resources, configurations, and state across clusters
- **Bulk Operations**: Execute maintenance tasks across entire fleet

### For Rancher Users
- **Environment Isolation**: Each MCP server instance paired with specific Rancher environment
- **Seamless Integration**: Works with existing NSK workflow and kubeconfig management
- **Dynamic Discovery**: Automatically picks up new clusters as they're added via NSK
- **Token Management**: Handles Rancher token rotation through NSK profiles
- **Familiar Patterns**: Leverages existing KUBECONFIG environment variable approach

### For Developers
- **Backward Compatibility**: Existing single-cluster usage patterns continue to work
- **Incremental Adoption**: Can gradually enable multi-cluster features
- **Flexible Configuration**: Support both directory-based and explicit configuration

## Risk Mitigation

### Security Considerations
- **Cluster Isolation**: Ensure operations don't accidentally cross cluster boundaries
- **Credential Management**: Secure handling of multiple cluster credentials
- **Access Control**: Respect per-cluster RBAC and access policies

### Operational Safety
- **Read-Only Mode**: Support read-only operations across all clusters
- **Destructive Operation Controls**: Enhanced safety for multi-cluster destructive operations
- **Audit Logging**: Track which operations run on which clusters

### Performance
- **Lazy Loading**: Only initialize cluster connections when needed
- **Connection Pooling**: Reuse cluster connections efficiently
- **Timeout Management**: Proper timeouts for multi-cluster operations

## Success Metrics

1. **Functionality**: All existing single-cluster operations work unchanged
2. **NSK Integration**: Successful pairing with specific Rancher environments via NSK
3. **Discovery**: Automatic detection of clusters from NSK kubeconfig directory
4. **Switching**: Ability to change active cluster context via MCP tools
5. **Cross-Cluster**: Successful execution of operations across multiple clusters
6. **Environment Isolation**: No cross-environment operations between MCP instances
7. **Auto-Refresh**: Clusters automatically discovered when added to Rancher
8. **Token Rotation**: Seamless handling of Rancher token updates via NSK profiles
9. **Performance**: No significant degradation in single-cluster operation performance

## Deployment Architecture

### Multi-Environment Deployment Strategy

```
┌─────────────────────┐    ┌─────────────────────┐    ┌─────────────────────┐
│   Production MCP    │    │    Staging MCP      │    │  Development MCP    │
│                     │    │                     │    │                     │
│ rancher.netskope.io │    │ rancher.prime.iad0  │    │ rancher-dev...      │
│ Profile: prod       │    │ Profile: staging    │    │ Profile: dev        │
│ Clusters: c1-*      │    │ Clusters: iad0-*    │    │ Clusters: dev-*     │
│ Port: 8080          │    │ Port: 8081          │    │ Port: 8082          │
└─────────────────────┘    └─────────────────────┘    └─────────────────────┘
          │                           │                           │
          ▼                           ▼                           ▼
┌─────────────────────┐    ┌─────────────────────┐    ┌─────────────────────┐
│ Production Clusters │    │  Staging Clusters   │    │ Development Clusters│
│ - c1-sv5           │    │ - iad0-sandbox      │    │ - dev-cluster       │
│ - c1-dfw1          │    │ - iad0-test         │    │ - local-dev         │
│ - c1-lax1          │    │ - staging-east      │    │ - feature-test      │
└─────────────────────┘    └─────────────────────┘    └─────────────────────┘
```

This proposal provides a comprehensive roadmap for implementing multi-cluster support while maintaining the existing architecture's strengths and ensuring seamless integration with your Rancher-based environment.