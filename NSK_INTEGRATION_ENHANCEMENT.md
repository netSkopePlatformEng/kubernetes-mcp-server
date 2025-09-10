# NSK Integration Enhancement for Multi-Cluster MCP Server

## Overview

This enhancement extends the multi-cluster proposal to properly integrate with NSK (Netskope Kubernetes) tool and Rancher environment variables, allowing each MCP server instance to be paired with a specific Rancher environment.

## NSK Environment Variables

Based on analysis of the NSK codebase, the following environment variables control NSK behavior:

### Core Rancher Configuration
- `RANCHER_URL` - Rancher server endpoint (e.g., "https://rancher.netskope.io")
- `RANCHER_TOKEN` - Bearer token for Rancher API authentication
- `RANCHER_INSECURE` - Skip SSL verification (true/false)
- `NSK_PROFILE` - Profile name from NSK configuration file (default: "default")
- `NSK_CONFDIR` - Directory for NSK configuration and kubeconfig files (default: "$HOME/.nsk")

### Extended Environment Variables
- `POLARIS_API_URL` - Polaris API endpoint
- `POLARIS_SSL_SKIP_VERIFY` - Skip SSL verification for Polaris
- `NETBOX_URL` - NetBox API endpoint
- `NETBOX_TOKEN` - NetBox API token
- `GITHUB_TOKEN` - GitHub personal access token

## Enhanced MCP Server Configuration

### New Configuration Fields

```go
// pkg/config/config.go
type StaticConfig struct {
    // Existing fields...
    KubeConfig string `toml:"kubeconfig,omitempty"`
    
    // Enhanced multi-cluster with NSK integration
    KubeConfigDir     string   `toml:"kubeconfig_dir,omitempty"`
    DefaultCluster    string   `toml:"default_cluster,omitempty"`
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

### Environment Variable Mapping

The MCP server should set NSK environment variables based on configuration:

```go
// pkg/kubernetes/nsk_integration.go
func (nsk *NSKIntegration) setEnvironment() error {
    envVars := map[string]string{
        "RANCHER_URL":      nsk.config.RancherURL,
        "RANCHER_TOKEN":    nsk.config.RancherToken,
        "RANCHER_INSECURE": strconv.FormatBool(nsk.config.RancherInsecure),
        "NSK_PROFILE":      nsk.config.Profile,
        "NSK_CONFDIR":      nsk.config.ConfigDir,
    }
    
    for key, value := range envVars {
        if value != "" {
            if err := os.Setenv(key, value); err != nil {
                return fmt.Errorf("failed to set %s: %w", key, err)
            }
        }
    }
    return nil
}
```

## Configuration Examples

### Per-Environment MCP Deployments

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

## Enhanced NSK Integration Service

```go
// pkg/kubernetes/nsk_integration.go
type NSKIntegration struct {
    config          *config.NSKConfig
    clusterManager  *ClusterManager
    refreshTicker   *time.Ticker
    stopChan        chan struct{}
    logger          klog.Logger
}

func NewNSKIntegration(cfg *config.NSKConfig, cm *ClusterManager) *NSKIntegration {
    return &NSKIntegration{
        config:         cfg,
        clusterManager: cm,
        stopChan:       make(chan struct{}),
        logger:         klog.Background(),
    }
}

func (nsk *NSKIntegration) Start(ctx context.Context) error {
    // Set environment variables
    if err := nsk.setEnvironment(); err != nil {
        return fmt.Errorf("failed to set NSK environment: %w", err)
    }
    
    // Initial refresh
    if err := nsk.RefreshKubeConfigs(ctx); err != nil {
        nsk.logger.Error(err, "Initial kubeconfig refresh failed")
    }
    
    // Start auto-refresh if enabled
    if nsk.config.AutoRefresh {
        interval, err := time.ParseDuration(nsk.config.RefreshInterval)
        if err != nil {
            interval = time.Hour // default to 1 hour
        }
        
        nsk.refreshTicker = time.NewTicker(interval)
        go nsk.refreshLoop(ctx)
    }
    
    return nil
}

func (nsk *NSKIntegration) RefreshKubeConfigs(ctx context.Context) error {
    nsk.logger.Info("Refreshing kubeconfigs from Rancher via NSK")
    
    cmd := exec.CommandContext(ctx, nsk.getNSKPath(), "cluster", "kubeconfig")
    if nsk.config.ClusterPattern != "" {
        // Add pattern filtering if needed
        cmd.Args = append(cmd.Args, "--name-pattern", nsk.config.ClusterPattern)
    }
    
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("NSK kubeconfig refresh failed: %w, output: %s", err, output)
    }
    
    nsk.logger.Info("NSK kubeconfig refresh completed", "output", string(output))
    
    // Trigger cluster manager to rescan directory
    return nsk.clusterManager.DiscoverClusters()
}

func (nsk *NSKIntegration) refreshLoop(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-nsk.stopChan:
            return
        case <-nsk.refreshTicker.C:
            if err := nsk.RefreshKubeConfigs(ctx); err != nil {
                nsk.logger.Error(err, "Auto-refresh of kubeconfigs failed")
            }
        }
    }
}

func (nsk *NSKIntegration) Stop() {
    if nsk.refreshTicker != nil {
        nsk.refreshTicker.Stop()
    }
    close(nsk.stopChan)
}

func (nsk *NSKIntegration) getNSKPath() string {
    if nsk.config.NSKPath != "" {
        return nsk.config.NSKPath
    }
    return "nsk" // default to PATH lookup
}
```

## New MCP Tools for NSK Integration

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

## Deployment Patterns

### Docker Compose for Multiple Environments

```yaml
# docker-compose.yml
version: '3.8'
services:
  mcp-production:
    image: kubernetes-mcp-server:latest
    environment:
      - PROD_RANCHER_TOKEN=${PROD_RANCHER_TOKEN}
    volumes:
      - ./config-production.toml:/etc/mcp/config.toml
      - ./clusters/prod:/etc/kubernetes/clusters/prod
    ports:
      - "8080:8080"
    command: ["--config", "/etc/mcp/config.toml", "--port", "8080"]
  
  mcp-staging:
    image: kubernetes-mcp-server:latest
    environment:
      - STAGING_RANCHER_TOKEN=${STAGING_RANCHER_TOKEN}
    volumes:
      - ./config-staging.toml:/etc/mcp/config.toml
      - ./clusters/staging:/etc/kubernetes/clusters/staging
    ports:
      - "8081:8080"
    command: ["--config", "/etc/mcp/config.toml", "--port", "8080"]
  
  mcp-development:
    image: kubernetes-mcp-server:latest
    environment:
      - DEV_RANCHER_TOKEN=${DEV_RANCHER_TOKEN}
    volumes:
      - ./config-development.toml:/etc/mcp/config.toml
      - ./clusters/dev:/etc/kubernetes/clusters/dev
    ports:
      - "8082:8080"
    command: ["--config", "/etc/mcp/config.toml", "--port", "8080"]
```

### Kubernetes Deployment

```yaml
# k8s-mcp-production.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kubernetes-mcp-server-production
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kubernetes-mcp-server
      environment: production
  template:
    metadata:
      labels:
        app: kubernetes-mcp-server
        environment: production
    spec:
      containers:
      - name: mcp-server
        image: kubernetes-mcp-server:latest
        args:
          - --config
          - /etc/mcp/config.toml
          - --port
          - "8080"
        env:
        - name: PROD_RANCHER_TOKEN
          valueFrom:
            secretKeyRef:
              name: rancher-tokens
              key: production-token
        volumeMounts:
        - name: config
          mountPath: /etc/mcp
        - name: kubeconfigs
          mountPath: /etc/kubernetes/clusters/prod
        ports:
        - containerPort: 8080
      volumes:
      - name: config
        configMap:
          name: mcp-config-production
      - name: kubeconfigs
        emptyDir: {}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: mcp-config-production
data:
  config.toml: |
    [nsk]
    rancher_url = "https://rancher.netskope.io"
    rancher_token = "${PROD_RANCHER_TOKEN}"
    profile = "prod"
    config_dir = "/etc/kubernetes/clusters/prod"
    auto_refresh = true
    refresh_interval = "1h"
    
    kubeconfig_dir = "/etc/kubernetes/clusters/prod"
    default_cluster = "c1-sv5"
    auto_discovery = true
```

## Command Line Enhancements

### New CLI Flags

```go
// pkg/kubernetes-mcp-server/cmd/root.go additions
cmd.Flags().StringVar(&o.NSKRancherURL, "nsk-rancher-url", "", "Rancher URL for NSK integration")
cmd.Flags().StringVar(&o.NSKProfile, "nsk-profile", "default", "NSK profile to use")
cmd.Flags().StringVar(&o.NSKConfigDir, "nsk-config-dir", "", "NSK configuration directory")
cmd.Flags().BoolVar(&o.NSKAutoRefresh, "nsk-auto-refresh", false, "Enable automatic kubeconfig refresh")
cmd.Flags().StringVar(&o.NSKRefreshInterval, "nsk-refresh-interval", "1h", "Kubeconfig refresh interval")
```

### Usage Examples

```bash
# Production MCP server
kubernetes-mcp-server \
  --config config-production.toml \
  --port 8080

# Development with override
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

## Benefits

### Environment Isolation
- Each MCP server instance is tied to a specific Rancher environment
- Clear separation between production, staging, and development clusters
- No risk of cross-environment operations

### Automatic Synchronization
- Kubeconfigs automatically refresh as clusters are added/removed in Rancher
- Token rotation handled by NSK configuration
- No manual intervention required for cluster discovery

### Flexible Deployment
- Support for multiple Rancher servers (prod, staging, dev)
- Container-friendly configuration
- Environment variable and file-based configuration options

### AI Agent Benefits
- Environment-aware cluster operations
- Automatic discovery of new clusters in the target environment
- Consistent cluster naming and organization per environment

This enhancement ensures that each MCP server instance is properly paired with its designated Rancher environment, providing secure and organized multi-cluster access for AI agents while maintaining clear environment boundaries.