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

func (nsk *NSKIntegration) GetClusterKubeConfig(ctx context.Context, clusterName string, save bool) (string, error) {
    nsk.logger.Info("Getting kubeconfig for cluster", "cluster", clusterName)
    
    cmd := exec.CommandContext(ctx, nsk.getNSKPath(), "cluster", "kubeconfig", "--name", clusterName)
    if !save {
        // Output to stdout instead of saving to file
        cmd.Args = append(cmd.Args, "--stdout")
    }
    
    output, err := cmd.CombinedOutput()
    if err != nil {
        return "", fmt.Errorf("NSK get kubeconfig failed for cluster %s: %w, output: %s", clusterName, err, output)
    }
    
    if save {
        nsk.logger.Info("NSK kubeconfig saved for cluster", "cluster", clusterName, "output", string(output))
        // Trigger cluster manager to rescan directory to pick up new file
        if err := nsk.clusterManager.DiscoverClusters(); err != nil {
            nsk.logger.Error(err, "Failed to rediscover clusters after kubeconfig save")
        }
        return string(output), nil
    }
    
    nsk.logger.Info("NSK kubeconfig retrieved for cluster", "cluster", clusterName)
    return string(output), nil
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
        
        {Tool: mcp.NewTool("nsk_get",
            mcp.WithDescription("Get kubeconfig for a specific cluster from Rancher via NSK"),
            mcp.WithString("cluster_name", mcp.Description("Name of the cluster to get kubeconfig for"), mcp.Required()),
            mcp.WithBoolean("save", mcp.Description("Save kubeconfig to file in config directory")),
            mcp.WithTitleAnnotation("NSK: Get Cluster Kubeconfig"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.nskGet},
    }
}
```

## Cluster Status and Visibility Tools

### Core Status Tool

```go
// pkg/mcp/clusters_status.go
func (s *Server) initClusterStatusTools() []server.ServerTool {
    return []server.ServerTool{
        {Tool: mcp.NewTool("clusters_status",
            mcp.WithDescription("Check the status and connectivity of Kubernetes clusters"),
            mcp.WithString("cluster", mcp.Description("Specific cluster to check (optional, checks all if not provided)")),
            mcp.WithTitleAnnotation("Clusters: Status"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.clustersStatus},

        {Tool: mcp.NewTool("clusters_refresh_status",
            mcp.WithDescription("Get detailed refresh status and history"),
            mcp.WithTitleAnnotation("Clusters: Refresh Status"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.clustersRefreshStatus},
    }
}

// Implementation
func (s *Server) clustersStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
    clusterName, _ := args["cluster"].(string)

    var results []ClusterStatus
    if clusterName != "" {
        // Check specific cluster
        status := s.checkClusterStatus(ctx, clusterName)
        results = append(results, status)
    } else {
        // Check all clusters in parallel
        results = s.checkAllClustersStatus(ctx)
    }

    // Format output as table
    output := formatClusterStatusTable(results)
    return mcp.NewToolResultText(output), nil
}

type ClusterStatus struct {
    Name           string
    Status         string // Ready, NotReady, Unknown
    Version        string
    NodeCount      int
    ReadyNodes     int
    NamespaceCount int
    KubeconfigAge  time.Duration
    LastError      string
    IsActive       bool
    APILatency     time.Duration
}

func (s *Server) checkClusterStatus(ctx context.Context, clusterName string) ClusterStatus {
    status := ClusterStatus{
        Name:   clusterName,
        Status: "Unknown",
    }

    // Get cluster config
    cluster, err := s.clusterManager.GetCluster(clusterName)
    if err != nil {
        status.LastError = fmt.Sprintf("cluster not found: %v", err)
        return status
    }

    // Check kubeconfig age
    if fileInfo, err := os.Stat(cluster.KubeConfigPath); err == nil {
        status.KubeconfigAge = time.Since(fileInfo.ModTime())
    }

    // Create timeout context for health check
    checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    // Test API connectivity
    start := time.Now()
    version, err := cluster.Manager.Discovery().ServerVersion()
    status.APILatency = time.Since(start)

    if err != nil {
        status.Status = "NotReady"
        status.LastError = err.Error()
        return status
    }

    status.Status = "Ready"
    status.Version = version.GitVersion

    // Get node status
    nodes, err := cluster.Manager.CoreV1().Nodes().List(checkCtx, metav1.ListOptions{})
    if err == nil {
        status.NodeCount = len(nodes.Items)
        for _, node := range nodes.Items {
            for _, condition := range node.Status.Conditions {
                if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue {
                    status.ReadyNodes++
                    break
                }
            }
        }
    }

    // Get namespace count
    namespaces, err := cluster.Manager.CoreV1().Namespaces().List(checkCtx, metav1.ListOptions{})
    if err == nil {
        status.NamespaceCount = len(namespaces.Items)
    }

    // Check if this is the active cluster
    status.IsActive = (clusterName == s.clusterManager.GetActiveCluster())

    return status
}

func formatClusterStatusTable(statuses []ClusterStatus) string {
    var buf bytes.Buffer
    w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

    // Header
    fmt.Fprintln(w, "CLUSTER\tSTATUS\tVERSION\tNODES\tNAMESPACES\tKUBECONFIG-AGE\tLATENCY\tERRORS")
    fmt.Fprintln(w, "-------\t------\t-------\t-----\t----------\t--------------\t-------\t------")

    for _, s := range statuses {
        cluster := s.Name
        if s.IsActive {
            cluster = cluster + " *"
        }

        nodes := "-"
        if s.NodeCount > 0 {
            nodes = fmt.Sprintf("%d/%d", s.ReadyNodes, s.NodeCount)
        }

        namespaces := "-"
        if s.NamespaceCount > 0 {
            namespaces = fmt.Sprintf("%d", s.NamespaceCount)
        }

        age := humanizeDuration(s.KubeconfigAge)
        latency := "-"
        if s.APILatency > 0 {
            latency = fmt.Sprintf("%dms", s.APILatency.Milliseconds())
        }

        errors := "-"
        if s.LastError != "" {
            // Truncate long error messages
            if len(s.LastError) > 40 {
                errors = s.LastError[:37] + "..."
            } else {
                errors = s.LastError
            }
        }

        fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
            cluster, s.Status, s.Version, nodes, namespaces, age, latency, errors)
    }

    w.Flush()
    buf.WriteString("\n* = current active cluster\n")
    return buf.String()
}
```

### Example Output

```bash
# From Claude CLI
> mcp clusters_status

CLUSTER         STATUS    VERSION   NODES   NAMESPACES  KUBECONFIG-AGE  LATENCY  ERRORS
-------         ------    -------   -----   ----------  --------------  -------  ------
c1-sv5          Ready     v1.24.17  15/15   45          2h ago          45ms     -
c1-dfw1         Ready     v1.24.17  12/12   42          2h ago          52ms     -
c1-lax1    *    Ready     v1.24.17  18/18   48          2h ago          38ms     -
iad0-sandbox    NotReady  -         -       -           25h ago         -        connection refused
dev-cluster     Unknown   -         -       -           -               -        kubeconfig not found

* = current active cluster

# Check specific cluster with detailed view
> mcp clusters_status --cluster c1-sv5

Cluster: c1-sv5
Status: Ready
API Server: https://rancher.netskope.io/k8s/clusters/c-m-xxxxx
Version: v1.24.17
Nodes: 15 (15 Ready, 0 NotReady)
  - Control Plane: 3
  - Workers: 12
Namespaces: 45
Resources:
  - Pods: 342 (338 Running, 4 Pending)
  - Services: 87
  - Deployments: 125
  - StatefulSets: 23
Kubeconfig: /etc/kubernetes/clusters/prod/c1-sv5.yaml
Last Updated: 2025-10-09 14:30:00 (2h ago)
Next Refresh: 2025-10-09 17:30:00 (in 58m)
Connectivity: OK (latency: 45ms)
Certificate Expiry: 89 days

# Refresh status
> mcp clusters_refresh_status

NSK Integration Status
----------------------
Profile: prod
Rancher URL: https://rancher.netskope.io
Config Directory: /etc/kubernetes/clusters/prod
Auto-Refresh: Enabled (every 1h)

Last Refresh: 2025-10-09 14:30:00 (2h ago)
Next Refresh: 2025-10-09 17:30:00 (in 58m)
Status: Success

Refresh History:
  - 2025-10-09 14:30:00: Success (5 clusters updated)
  - 2025-10-09 13:30:00: Success (5 clusters updated)
  - 2025-10-09 12:30:00: Partial (4/5 clusters, iad0-sandbox failed)
  - 2025-10-09 11:30:00: Success (5 clusters updated)

Clusters:
  - c1-sv5: Active, last seen 2h ago
  - c1-dfw1: Active, last seen 2h ago
  - c1-lax1: Active, last seen 2h ago
  - iad0-sandbox: Inactive, connection issues since 25h ago
  - dev-cluster: Removed (not in Rancher anymore)
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

### Operational Visibility
- Real-time cluster health monitoring via `clusters_status`
- Refresh history and status tracking
- Clear error reporting for troubleshooting
- API latency monitoring per cluster

## Security & File System Protection

### Kubeconfig File Security

```go
// pkg/kubernetes/security.go
const (
    KubeconfigFileMode = 0600 // Read/write for owner only
    ConfigDirMode      = 0700 // Full access for owner only
)

func (nsk *NSKIntegration) secureKubeconfigFile(path string) error {
    // Ensure proper file permissions
    if err := os.Chmod(path, KubeconfigFileMode); err != nil {
        return fmt.Errorf("failed to secure kubeconfig file %s: %w", path, err)
    }

    // Verify ownership
    fileInfo, err := os.Stat(path)
    if err != nil {
        return err
    }

    // Log file access for audit
    nsk.logger.Info("Kubeconfig file secured",
        "path", path,
        "mode", fileInfo.Mode(),
        "size", fileInfo.Size())

    return nil
}

func (nsk *NSKIntegration) secureConfigDirectory(dir string) error {
    // Create directory with secure permissions
    if err := os.MkdirAll(dir, ConfigDirMode); err != nil {
        return fmt.Errorf("failed to create config directory: %w", err)
    }

    // Ensure existing directory has correct permissions
    if err := os.Chmod(dir, ConfigDirMode); err != nil {
        return fmt.Errorf("failed to secure config directory: %w", err)
    }

    return nil
}
```

### Sensitive Data Handling

```go
// pkg/kubernetes/sanitizer.go
type Sanitizer struct {
    patterns []*regexp.Regexp
}

func NewSanitizer() *Sanitizer {
    return &Sanitizer{
        patterns: []*regexp.Regexp{
            regexp.MustCompile(`(token|password|secret|key):\s*[^\s]+`),
            regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-\._~\+\/]+`),
            regexp.MustCompile(`-----BEGIN [A-Z ]+-----[\s\S]+?-----END [A-Z ]+-----`),
        },
    }
}

func (s *Sanitizer) SanitizeString(input string) string {
    result := input
    for _, pattern := range s.patterns {
        result = pattern.ReplaceAllString(result, "[REDACTED]")
    }
    return result
}

// Custom logger that sanitizes sensitive data
type SecureLogger struct {
    logger    klog.Logger
    sanitizer *Sanitizer
}

func (sl *SecureLogger) Info(msg string, keysAndValues ...interface{}) {
    sanitized := make([]interface{}, len(keysAndValues))
    for i, v := range keysAndValues {
        if str, ok := v.(string); ok {
            sanitized[i] = sl.sanitizer.SanitizeString(str)
        } else {
            sanitized[i] = v
        }
    }
    sl.logger.Info(msg, sanitized...)
}
```

### Environment Variable Security

```go
// pkg/kubernetes/env_security.go
func (nsk *NSKIntegration) loadSecureEnvironment() error {
    // Load RANCHER_TOKEN from secure source if available
    if token := os.Getenv("RANCHER_TOKEN"); token == "" {
        // Try to load from Kubernetes secret if running in cluster
        if secret, err := nsk.loadFromK8sSecret(); err == nil {
            os.Setenv("RANCHER_TOKEN", secret)
        }
    }

    // Never log the actual token value
    nsk.logger.Info("Environment configured",
        "rancher_url", os.Getenv("RANCHER_URL"),
        "profile", os.Getenv("NSK_PROFILE"),
        "token_set", os.Getenv("RANCHER_TOKEN") != "")

    return nil
}

func (nsk *NSKIntegration) loadFromK8sSecret() (string, error) {
    // Implementation for loading from K8s secret
    // when running inside a cluster
    return "", nil
}
```

### Audit Logging

```go
// pkg/kubernetes/audit.go
type AuditLogger struct {
    file *os.File
}

func NewAuditLogger(path string) (*AuditLogger, error) {
    file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
    if err != nil {
        return nil, err
    }
    return &AuditLogger{file: file}, nil
}

func (al *AuditLogger) LogClusterAccess(user, cluster, operation string) {
    entry := fmt.Sprintf("%s|%s|%s|%s\n",
        time.Now().Format(time.RFC3339),
        user,
        cluster,
        operation)
    al.file.WriteString(entry)
}
```

## Error Handling & Resilience

### NSK Command Resilience

```go
// pkg/kubernetes/nsk_resilience.go
type NSKCommandExecutor struct {
    maxRetries     int
    initialBackoff time.Duration
    maxBackoff     time.Duration
    timeout        time.Duration
}

func NewNSKCommandExecutor() *NSKCommandExecutor {
    return &NSKCommandExecutor{
        maxRetries:     3,
        initialBackoff: 1 * time.Second,
        maxBackoff:     30 * time.Second,
        timeout:        30 * time.Second,
    }
}

func (e *NSKCommandExecutor) ExecuteWithRetry(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
    backoff := e.initialBackoff

    for attempt := 0; attempt <= e.maxRetries; attempt++ {
        if attempt > 0 {
            select {
            case <-ctx.Done():
                return nil, ctx.Err()
            case <-time.After(backoff):
                // Exponential backoff
                backoff = time.Duration(math.Min(float64(backoff*2), float64(e.maxBackoff)))
            }
        }

        // Create timeout context for this attempt
        cmdCtx, cancel := context.WithTimeout(ctx, e.timeout)
        defer cancel()

        cmd.Context = cmdCtx
        output, err := cmd.CombinedOutput()

        if err == nil {
            return output, nil
        }

        // Check if error is retryable
        if !isRetryableError(err) {
            return output, err
        }

        klog.Warningf("NSK command failed (attempt %d/%d): %v", attempt+1, e.maxRetries+1, err)
    }

    return nil, fmt.Errorf("NSK command failed after %d attempts", e.maxRetries+1)
}

func isRetryableError(err error) bool {
    // Network errors, timeouts, and temporary failures are retryable
    if os.IsTimeout(err) {
        return true
    }

    errStr := err.Error()
    retryablePatterns := []string{
        "connection refused",
        "network unreachable",
        "timeout",
        "temporary failure",
        "rate limit",
    }

    for _, pattern := range retryablePatterns {
        if strings.Contains(strings.ToLower(errStr), pattern) {
            return true
        }
    }

    return false
}
```

### Graceful Degradation

```go
// pkg/kubernetes/fallback.go
type ClusterFallbackManager struct {
    cache          *KubeconfigCache
    healthTracker  *ClusterHealthTracker
}

func (fm *ClusterFallbackManager) GetKubeconfig(ctx context.Context, clusterName string) (*rest.Config, error) {
    // Try to get fresh kubeconfig
    config, err := fm.getFreshKubeconfig(ctx, clusterName)
    if err == nil {
        fm.cache.Update(clusterName, config)
        return config, nil
    }

    // Fall back to cached version if available
    if cached, ok := fm.cache.Get(clusterName); ok {
        klog.Warningf("Using cached kubeconfig for cluster %s due to error: %v", clusterName, err)
        fm.healthTracker.MarkDegraded(clusterName, "using cached config")
        return cached, nil
    }

    // No cached version available
    fm.healthTracker.MarkUnhealthy(clusterName, err.Error())
    return nil, fmt.Errorf("no kubeconfig available for cluster %s: %w", clusterName, err)
}
```

### Cluster Health Tracking

```go
// pkg/kubernetes/health.go
type ClusterHealthTracker struct {
    statuses map[string]*ClusterHealth
    mu       sync.RWMutex
}

type ClusterHealth struct {
    Status        HealthStatus
    LastHealthy   time.Time
    LastChecked   time.Time
    ErrorCount    int
    LastError     string
    Degraded      bool
    DegradedReason string
}

type HealthStatus int

const (
    HealthStatusUnknown HealthStatus = iota
    HealthStatusHealthy
    HealthStatusDegraded
    HealthStatusUnhealthy
)

func (ht *ClusterHealthTracker) RecordSuccess(clusterName string) {
    ht.mu.Lock()
    defer ht.mu.Unlock()

    if health, ok := ht.statuses[clusterName]; ok {
        health.Status = HealthStatusHealthy
        health.LastHealthy = time.Now()
        health.LastChecked = time.Now()
        health.ErrorCount = 0
        health.LastError = ""
        health.Degraded = false
        health.DegradedReason = ""
    }
}

func (ht *ClusterHealthTracker) RecordFailure(clusterName string, err error) {
    ht.mu.Lock()
    defer ht.mu.Unlock()

    health, ok := ht.statuses[clusterName]
    if !ok {
        health = &ClusterHealth{}
        ht.statuses[clusterName] = health
    }

    health.LastChecked = time.Now()
    health.ErrorCount++
    health.LastError = err.Error()

    // Mark unhealthy after 3 consecutive failures
    if health.ErrorCount >= 3 {
        health.Status = HealthStatusUnhealthy
    }
}
```

## Performance & Scalability

### Connection Pooling

```go
// pkg/kubernetes/connection_pool.go
type ConnectionPool struct {
    connections map[string]*kubernetes.Clientset
    configs     map[string]*rest.Config
    mu          sync.RWMutex
    maxIdle     time.Duration
    lastUsed    map[string]time.Time
}

func NewConnectionPool() *ConnectionPool {
    pool := &ConnectionPool{
        connections: make(map[string]*kubernetes.Clientset),
        configs:     make(map[string]*rest.Config),
        lastUsed:    make(map[string]time.Time),
        maxIdle:     5 * time.Minute,
    }

    // Start cleanup goroutine
    go pool.cleanupIdleConnections()

    return pool
}

func (p *ConnectionPool) GetClient(clusterName string) (*kubernetes.Clientset, error) {
    p.mu.Lock()
    defer p.mu.Unlock()

    // Update last used time
    p.lastUsed[clusterName] = time.Now()

    // Return existing connection if available
    if client, ok := p.connections[clusterName]; ok {
        return client, nil
    }

    // Create new connection
    config, err := p.getConfig(clusterName)
    if err != nil {
        return nil, err
    }

    client, err := kubernetes.NewForConfig(config)
    if err != nil {
        return nil, err
    }

    p.connections[clusterName] = client
    return client, nil
}

func (p *ConnectionPool) cleanupIdleConnections() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()

    for range ticker.C {
        p.mu.Lock()
        now := time.Now()

        for cluster, lastUsed := range p.lastUsed {
            if now.Sub(lastUsed) > p.maxIdle {
                delete(p.connections, cluster)
                delete(p.configs, cluster)
                delete(p.lastUsed, cluster)
                klog.V(2).Infof("Cleaned up idle connection for cluster %s", cluster)
            }
        }

        p.mu.Unlock()
    }
}
```

### Parallel Operations

```go
// pkg/kubernetes/parallel.go
type ParallelExecutor struct {
    maxConcurrency int
    semaphore      chan struct{}
}

func NewParallelExecutor(maxConcurrency int) *ParallelExecutor {
    return &ParallelExecutor{
        maxConcurrency: maxConcurrency,
        semaphore:      make(chan struct{}, maxConcurrency),
    }
}

func (pe *ParallelExecutor) CheckAllClustersStatus(ctx context.Context, clusters []string) []ClusterStatus {
    results := make([]ClusterStatus, len(clusters))
    var wg sync.WaitGroup

    for i, cluster := range clusters {
        wg.Add(1)
        go func(idx int, clusterName string) {
            defer wg.Done()

            // Acquire semaphore
            pe.semaphore <- struct{}{}
            defer func() { <-pe.semaphore }()

            // Check cluster status with timeout
            checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
            defer cancel()

            results[idx] = checkClusterStatus(checkCtx, clusterName)
        }(i, cluster)
    }

    wg.Wait()
    return results
}
```

### Smart Caching

```go
// pkg/kubernetes/cache.go
type KubeconfigCache struct {
    cache      map[string]*CacheEntry
    mu         sync.RWMutex
    ttl        time.Duration
    fileWatcher *fsnotify.Watcher
}

type CacheEntry struct {
    Config      *rest.Config
    LoadedAt    time.Time
    FileModTime time.Time
    FilePath    string
}

func NewKubeconfigCache(ttl time.Duration) (*KubeconfigCache, error) {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, err
    }

    cache := &KubeconfigCache{
        cache:       make(map[string]*CacheEntry),
        ttl:         ttl,
        fileWatcher: watcher,
    }

    go cache.watchFiles()

    return cache, nil
}

func (kc *KubeconfigCache) Get(clusterName string) (*rest.Config, bool) {
    kc.mu.RLock()
    defer kc.mu.RUnlock()

    entry, ok := kc.cache[clusterName]
    if !ok {
        return nil, false
    }

    // Check if cache entry is still valid
    if time.Since(entry.LoadedAt) > kc.ttl {
        return nil, false
    }

    // Check if file has been modified
    if fileInfo, err := os.Stat(entry.FilePath); err == nil {
        if fileInfo.ModTime().After(entry.FileModTime) {
            return nil, false
        }
    }

    return entry.Config, true
}

func (kc *KubeconfigCache) watchFiles() {
    for {
        select {
        case event, ok := <-kc.fileWatcher.Events:
            if !ok {
                return
            }

            if event.Op&fsnotify.Write == fsnotify.Write {
                kc.invalidateByPath(event.Name)
            }

        case err, ok := <-kc.fileWatcher.Errors:
            if !ok {
                return
            }
            klog.Errorf("File watcher error: %v", err)
        }
    }
}
```

## Monitoring & Observability

### Prometheus Metrics

```go
// pkg/metrics/metrics.go
var (
    clusterTotal = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "kubernetes_mcp_cluster_total",
            Help: "Total number of discovered clusters",
        },
        []string{"environment"},
    )

    clusterHealthy = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "kubernetes_mcp_cluster_healthy",
            Help: "Cluster health status (1=healthy, 0=unhealthy)",
        },
        []string{"cluster", "environment"},
    )

    refreshSuccess = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "kubernetes_mcp_refresh_success_total",
            Help: "Total successful kubeconfig refreshes",
        },
        []string{"environment"},
    )

    refreshFailure = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "kubernetes_mcp_refresh_failure_total",
            Help: "Total failed kubeconfig refreshes",
        },
        []string{"environment", "reason"},
    )

    refreshDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "kubernetes_mcp_refresh_duration_seconds",
            Help:    "Duration of kubeconfig refresh operations",
            Buckets: prometheus.DefBuckets,
        },
        []string{"environment"},
    )

    clusterAPILatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "kubernetes_mcp_cluster_api_latency_seconds",
            Help:    "API server latency per cluster",
            Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
        },
        []string{"cluster", "operation"},
    )
)

func init() {
    prometheus.MustRegister(
        clusterTotal,
        clusterHealthy,
        refreshSuccess,
        refreshFailure,
        refreshDuration,
        clusterAPILatency,
    )
}
```

### Health Check Endpoints

```go
// pkg/http/health.go
type HealthHandler struct {
    clusterManager *kubernetes.ClusterManager
    nsk           *kubernetes.NSKIntegration
}

func (h *HealthHandler) LivenessHandler(w http.ResponseWriter, r *http.Request) {
    // Basic liveness check - is the process running?
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{
        "status": "alive",
        "timestamp": time.Now().Format(time.RFC3339),
    })
}

func (h *HealthHandler) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
    ready := true
    details := make(map[string]interface{})

    // Check if we have at least one healthy cluster
    clusters := h.clusterManager.ListClusters()
    healthyClusters := 0

    for _, cluster := range clusters {
        if cluster.IsHealthy() {
            healthyClusters++
        }
    }

    details["total_clusters"] = len(clusters)
    details["healthy_clusters"] = healthyClusters

    if healthyClusters == 0 && len(clusters) > 0 {
        ready = false
        details["reason"] = "no healthy clusters available"
    }

    // Check NSK integration if configured
    if h.nsk != nil {
        details["nsk_configured"] = true
        details["last_refresh"] = h.nsk.LastRefreshTime()
        details["next_refresh"] = h.nsk.NextRefreshTime()
    }

    if ready {
        w.WriteHeader(http.StatusOK)
        details["status"] = "ready"
    } else {
        w.WriteHeader(http.StatusServiceUnavailable)
        details["status"] = "not ready"
    }

    json.NewEncoder(w).Encode(details)
}
```

### Structured Logging

```go
// pkg/logging/structured.go
type StructuredLogger struct {
    base klog.Logger
}

func NewStructuredLogger() *StructuredLogger {
    return &StructuredLogger{
        base: klog.Background(),
    }
}

func (sl *StructuredLogger) LogClusterOperation(operation string, cluster string, duration time.Duration, err error) {
    fields := []interface{}{
        "operation", operation,
        "cluster", cluster,
        "duration_ms", duration.Milliseconds(),
        "timestamp", time.Now().Unix(),
    }

    if err != nil {
        fields = append(fields, "error", err.Error())
        sl.base.Error(err, "Cluster operation failed", fields...)
    } else {
        fields = append(fields, "status", "success")
        sl.base.Info("Cluster operation completed", fields...)
    }

    // Update metrics
    clusterAPILatency.WithLabelValues(cluster, operation).Observe(duration.Seconds())
}
```

## Testing Strategy

### Mock NSK Implementation

```go
// pkg/kubernetes/nsk_mock.go
type MockNSK struct {
    clusters      []string
    shouldFail    bool
    failureReason string
    refreshCount  int
    mu            sync.Mutex
}

func NewMockNSK(clusters []string) *MockNSK {
    return &MockNSK{
        clusters: clusters,
    }
}

func (m *MockNSK) RefreshKubeConfigs(ctx context.Context) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.refreshCount++

    if m.shouldFail {
        return fmt.Errorf("mock refresh failed: %s", m.failureReason)
    }

    // Simulate writing kubeconfig files
    for _, cluster := range m.clusters {
        content := generateMockKubeconfig(cluster)
        path := filepath.Join("/tmp/test-clusters", cluster+".yaml")
        if err := os.WriteFile(path, []byte(content), 0600); err != nil {
            return err
        }
    }

    return nil
}

func (m *MockNSK) GetClusterList() ([]string, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.shouldFail {
        return nil, fmt.Errorf("mock list failed: %s", m.failureReason)
    }

    return m.clusters, nil
}

func (m *MockNSK) SimulateFailure(reason string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.shouldFail = true
    m.failureReason = reason
}
```

### Integration Tests

```go
// pkg/kubernetes/integration_test.go
func TestMultiClusterOperations(t *testing.T) {
    // Setup test environment
    testClusters := []string{"test-cluster-1", "test-cluster-2", "test-cluster-3"}
    mockNSK := NewMockNSK(testClusters)

    config := &config.NSKConfig{
        ConfigDir:       "/tmp/test-clusters",
        AutoRefresh:     true,
        RefreshInterval: "5s",
    }

    clusterManager := NewClusterManager(config.ConfigDir, nil)
    nsk := NewNSKIntegration(config, clusterManager)
    nsk.nsk = mockNSK // Replace with mock

    ctx := context.Background()

    // Test initial refresh
    err := nsk.RefreshKubeConfigs(ctx)
    require.NoError(t, err)

    // Verify clusters discovered
    clusters := clusterManager.ListClusters()
    assert.Len(t, clusters, len(testClusters))

    // Test cluster switching
    err = clusterManager.SetActiveCluster("test-cluster-2")
    require.NoError(t, err)

    active := clusterManager.GetActiveCluster()
    assert.Equal(t, "test-cluster-2", active)

    // Test failure handling
    mockNSK.SimulateFailure("network error")
    err = nsk.RefreshKubeConfigs(ctx)
    assert.Error(t, err)

    // Verify cached configs still available
    clusters = clusterManager.ListClusters()
    assert.Len(t, clusters, len(testClusters))
}
```

### Load Testing

```go
// pkg/kubernetes/load_test.go
func BenchmarkParallelClusterStatus(b *testing.B) {
    // Create many mock clusters
    numClusters := 50
    clusters := make([]string, numClusters)
    for i := 0; i < numClusters; i++ {
        clusters[i] = fmt.Sprintf("cluster-%d", i)
    }

    executor := NewParallelExecutor(10) // Max 10 concurrent
    ctx := context.Background()

    b.ResetTimer()

    for i := 0; i < b.N; i++ {
        results := executor.CheckAllClustersStatus(ctx, clusters)
        if len(results) != numClusters {
            b.Fatalf("Expected %d results, got %d", numClusters, len(results))
        }
    }

    b.ReportMetric(float64(numClusters), "clusters/op")
}
```

## Configuration Validation

```go
// pkg/config/validation.go
type ConfigValidator struct {
    errors []string
}

func (v *ConfigValidator) Validate(cfg *NSKConfig) error {
    v.errors = []string{}

    // Check NSK binary
    v.validateNSKBinary(cfg.NSKPath)

    // Check directory permissions
    v.validateDirectory(cfg.ConfigDir)

    // Validate refresh interval
    v.validateRefreshInterval(cfg.RefreshInterval)

    // Check environment variables
    v.validateEnvironment()

    // Validate cluster patterns
    v.validateClusterPattern(cfg.ClusterPattern)

    if len(v.errors) > 0 {
        return fmt.Errorf("configuration validation failed:\n%s", strings.Join(v.errors, "\n"))
    }

    return nil
}

func (v *ConfigValidator) validateNSKBinary(path string) {
    if path == "" {
        path = "nsk"
    }

    fullPath, err := exec.LookPath(path)
    if err != nil {
        v.errors = append(v.errors, fmt.Sprintf("NSK binary not found: %s", path))
        return
    }

    // Check if executable
    if info, err := os.Stat(fullPath); err == nil {
        if info.Mode()&0111 == 0 {
            v.errors = append(v.errors, fmt.Sprintf("NSK binary not executable: %s", fullPath))
        }
    }
}

func (v *ConfigValidator) validateDirectory(dir string) {
    if dir == "" {
        v.errors = append(v.errors, "Config directory not specified")
        return
    }

    // Check if directory exists or can be created
    if err := os.MkdirAll(dir, 0700); err != nil {
        v.errors = append(v.errors, fmt.Sprintf("Cannot create config directory: %v", err))
        return
    }

    // Check write permissions
    testFile := filepath.Join(dir, ".test-write")
    if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
        v.errors = append(v.errors, fmt.Sprintf("Config directory not writable: %v", err))
    } else {
        os.Remove(testFile)
    }
}
```

## Troubleshooting Guide

### Common Issues and Solutions

#### NSK Command Failures

**Problem**: NSK commands timing out or failing
```bash
# Debug NSK commands manually
NSK_DEBUG=true nsk cluster list
nsk cluster kubeconfig --name cluster-name --stdout
```

**Solutions**:
1. Check Rancher connectivity: `curl -I $RANCHER_URL`
2. Verify token validity: `nsk auth verify`
3. Check network/proxy settings
4. Increase timeout in config

#### Kubeconfig Validation

**Problem**: Invalid or corrupted kubeconfig files
```bash
# Validate kubeconfig manually
kubectl --kubeconfig=/path/to/kubeconfig.yaml cluster-info
```

**Solutions**:
1. Force refresh: `mcp nsk_refresh --force`
2. Check file permissions: `ls -la ~/.nsk/`
3. Validate YAML syntax
4. Remove and re-download specific cluster config

#### Permission Issues

**Problem**: Permission denied errors
```bash
# Check file and directory permissions
ls -la ~/.nsk/
stat ~/.nsk/*.yaml
```

**Solutions**:
1. Fix directory permissions: `chmod 700 ~/.nsk`
2. Fix file permissions: `chmod 600 ~/.nsk/*.yaml`
3. Check user ownership
4. Verify SELinux/AppArmor policies if applicable

#### Connectivity Verification

**Problem**: Cannot connect to clusters
```bash
# Test cluster connectivity
for cluster in $(ls ~/.nsk/*.yaml | xargs -n1 basename | sed 's/.yaml//'); do
    echo "Testing $cluster..."
    kubectl --kubeconfig ~/.nsk/$cluster.yaml get nodes --request-timeout=5s
done
```

### Debug Logging

Enable verbose logging for troubleshooting:
```bash
# Set log level
kubernetes-mcp-server --v=4 --kubeconfig-dir ~/.nsk

# With debug output
DEBUG=true kubernetes-mcp-server --kubeconfig-dir ~/.nsk
```

### Log Analysis

Key log patterns to look for:
```bash
# Check for refresh failures
grep "refresh failed" /var/log/mcp-server.log

# Find cluster connection errors
grep "cluster.*connection" /var/log/mcp-server.log

# Monitor health check failures
grep "health check.*failed" /var/log/mcp-server.log
```

## Implementation Roadmap

### Week 1: Core NSK Integration & Status Tools
- Implement basic NSK integration (`NSKIntegration` struct)
- Create `clusters_status` tool with health checking
- Add `clusters_refresh_status` tool
- Set up file system security (permissions)
- Basic configuration validation

**Deliverables**:
- Working NSK refresh mechanism
- Cluster status visibility in Claude CLI
- Secure file handling

### Week 2: Auto-Refresh & Error Handling
- Implement auto-refresh mechanism with timers
- Add retry logic with exponential backoff
- Create fallback to cached kubeconfigs
- Implement cluster health tracking
- Add graceful degradation

**Deliverables**:
- Resilient refresh system
- Automatic recovery from failures
- Health status tracking per cluster

### Week 3: Performance Optimization
- Implement connection pooling
- Add parallel cluster operations
- Create smart caching with TTL
- Add file watching for cache invalidation
- Optimize API calls with rate limiting

**Deliverables**:
- Sub-second cluster switching
- Efficient resource usage
- Scalable to 50+ clusters

### Week 4: Monitoring & Observability
- Add Prometheus metrics
- Implement health check endpoints
- Create structured logging
- Add audit logging
- Build debug mode

**Deliverables**:
- `/metrics` endpoint
- `/health` and `/ready` endpoints
- Comprehensive logging

### Week 5: Testing & Documentation
- Write unit tests with mock NSK
- Create integration tests
- Perform load testing
- Write troubleshooting guide
- Create migration documentation

**Deliverables**:
- 80%+ test coverage
- Performance benchmarks
- Complete documentation

### Week 6: Production Readiness
- Security audit
- Performance tuning
- Documentation review
- Deployment automation
- Handover and training

**Deliverables**:
- Production-ready release
- Deployment scripts
- Operations runbook

## Summary

This enhancement ensures that each MCP server instance is properly paired with its designated Rancher environment, providing secure and organized multi-cluster access for AI agents while maintaining clear environment boundaries. The implementation focuses on:

1. **Visibility**: Real-time cluster status monitoring via `clusters_status` tool
2. **Security**: Proper file permissions and sensitive data handling
3. **Resilience**: Graceful degradation and automatic recovery
4. **Performance**: Connection pooling and smart caching
5. **Observability**: Comprehensive metrics and logging
6. **Testing**: Thorough test coverage with mocks and integration tests

The phased implementation approach ensures incremental delivery of value while building toward a production-ready system.