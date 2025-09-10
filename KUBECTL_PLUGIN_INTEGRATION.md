# Kubectl Plugin Integration for Kubernetes MCP Server

## Overview

This proposal adds comprehensive kubectl plugin support to the Kubernetes MCP Server, enabling AI agents to discover, execute, and manage kubectl plugins seamlessly. The integration provides automatic plugin discovery, dynamic MCP tool generation, and secure execution within the multi-cluster environment.

## Kubectl Plugin Architecture Analysis

### Plugin Discovery Mechanism
Kubectl plugins follow a simple convention:
- **Naming**: Executables named `kubectl-<plugin-name>` in PATH
- **Discovery**: `kubectl plugin list` scans PATH for `kubectl-*` executables
- **Execution**: `kubectl <plugin-name> [args]` delegates to `kubectl-<plugin-name>`

### Common Plugin Locations
- `/usr/local/bin/kubectl-*` - System-wide plugins
- `~/.krew/bin/kubectl-*` - Krew-managed plugins
- `~/go/bin/kubectl-*` - Go-installed plugins
- Custom directories in PATH

### Plugin Examples Found
From system analysis:
- `kubectl-evict` - Pod eviction management
- `kubectl-cnpg` - CloudNativePG operations
- `kubectl-get_all` - Enhanced resource listing
- `kubectl-rook_ceph` - Rook Ceph management
- `kubectl-csi_scan` - CSI volume scanning

## Proposed Integration Architecture

### 1. Plugin Discovery Service

```go
// pkg/kubernetes/plugin_manager.go
type PluginManager struct {
    plugins         map[string]*PluginInfo
    kubectlPath     string
    allowedPlugins  []string
    blockedPlugins  []string
    refreshInterval time.Duration
    mutex           sync.RWMutex
    logger          klog.Logger
}

type PluginInfo struct {
    Name            string            `json:"name"`
    Path            string            `json:"path"`
    Description     string            `json:"description,omitempty"`
    Version         string            `json:"version,omitempty"`
    Aliases         []string          `json:"aliases,omitempty"`
    Parameters      []PluginParameter `json:"parameters,omitempty"`
    LastUsed        time.Time         `json:"last_used"`
    ExecutionCount  int               `json:"execution_count"`
    Cluster         string            `json:"cluster,omitempty"` // For cluster-specific plugins
}

type PluginParameter struct {
    Name        string `json:"name"`
    Type        string `json:"type"` // string, int, bool, array
    Required    bool   `json:"required"`
    Description string `json:"description,omitempty"`
    Default     string `json:"default,omitempty"`
}

func NewPluginManager(kubectlPath string, config *PluginConfig) *PluginManager
func (pm *PluginManager) DiscoverPlugins() error
func (pm *PluginManager) GetPlugin(name string) (*PluginInfo, error)
func (pm *PluginManager) ListPlugins() []PluginInfo
func (pm *PluginManager) ExecutePlugin(ctx context.Context, name string, args []string, cluster string) (*PluginResult, error)
func (pm *PluginManager) RefreshPlugins() error
```

### 2. Enhanced Configuration

```go
// pkg/config/config.go additions
type StaticConfig struct {
    // ... existing fields
    
    // Plugin Configuration
    PluginIntegration *PluginConfig `toml:"plugins,omitempty"`
}

type PluginConfig struct {
    // Plugin discovery settings
    Enabled           bool     `toml:"enabled,omitempty"`
    AutoDiscovery     bool     `toml:"auto_discovery,omitempty"`
    RefreshInterval   string   `toml:"refresh_interval,omitempty"` // e.g., "5m", "1h"
    
    // Security settings
    AllowedPlugins    []string `toml:"allowed_plugins,omitempty"`
    BlockedPlugins    []string `toml:"blocked_plugins,omitempty"`
    AllowAnyPlugin    bool     `toml:"allow_any_plugin,omitempty"`
    
    // Execution settings
    ExecutionTimeout  string   `toml:"execution_timeout,omitempty"` // e.g., "30s", "5m"
    MaxConcurrent     int      `toml:"max_concurrent,omitempty"`
    
    // Plugin paths
    PluginPaths       []string `toml:"plugin_paths,omitempty"`
    KubectlPath       string   `toml:"kubectl_path,omitempty"`
    
    // Cluster-specific plugins
    ClusterPlugins    map[string][]string `toml:"cluster_plugins,omitempty"`
}
```

### 3. Dynamic MCP Tool Generation

```go
// pkg/mcp/plugins.go
func (s *Server) initPluginTools() []server.ServerTool {
    if s.pluginManager == nil {
        return nil
    }
    
    var tools []server.ServerTool
    
    // Add plugin management tools
    tools = append(tools, s.createPluginManagementTools()...)
    
    // Add dynamic tools for each discovered plugin
    for _, plugin := range s.pluginManager.ListPlugins() {
        if s.isPluginAllowed(plugin.Name) {
            tools = append(tools, s.createPluginTool(plugin))
        }
    }
    
    return tools
}

func (s *Server) createPluginTool(plugin PluginInfo) server.ServerTool {
    toolName := fmt.Sprintf("plugin_%s", strings.ReplaceAll(plugin.Name, "-", "_"))
    
    tool := mcp.NewTool(toolName,
        mcp.WithDescription(fmt.Sprintf("Execute kubectl plugin: %s - %s", plugin.Name, plugin.Description)),
        mcp.WithString("cluster", mcp.Description("Cluster to execute plugin on (optional, uses active cluster)")),
    )
    
    // Add plugin-specific parameters
    for _, param := range plugin.Parameters {
        switch param.Type {
        case "string":
            if param.Required {
                tool = tool.WithString(param.Name, mcp.Description(param.Description), mcp.Required())
            } else {
                tool = tool.WithString(param.Name, mcp.Description(param.Description))
            }
        case "bool":
            tool = tool.WithBoolean(param.Name, mcp.Description(param.Description))
        case "array":
            tool = tool.WithArray(param.Name, mcp.Description(param.Description))
        }
    }
    
    // Add annotations
    tool = tool.WithTitleAnnotation(fmt.Sprintf("Plugin: %s", plugin.Name)).
        WithReadOnlyHintAnnotation(false).
        WithDestructiveHintAnnotation(s.isPluginDestructive(plugin.Name))
    
    return server.ServerTool{
        Tool:    tool,
        Handler: s.createPluginHandler(plugin),
    }
}

func (s *Server) createPluginHandler(plugin PluginInfo) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        args := req.GetArguments()
        
        // Extract cluster parameter
        cluster := ""
        if clusterArg, ok := args["cluster"].(string); ok {
            cluster = clusterArg
        }
        
        // Build plugin arguments
        var pluginArgs []string
        for _, param := range plugin.Parameters {
            if value, exists := args[param.Name]; exists && value != nil {
                switch v := value.(type) {
                case string:
                    if v != "" {
                        pluginArgs = append(pluginArgs, fmt.Sprintf("--%s=%s", param.Name, v))
                    }
                case bool:
                    if v {
                        pluginArgs = append(pluginArgs, fmt.Sprintf("--%s", param.Name))
                    }
                case []interface{}:
                    for _, item := range v {
                        pluginArgs = append(pluginArgs, fmt.Sprintf("--%s=%v", param.Name, item))
                    }
                }
            }
        }
        
        // Execute plugin
        result, err := s.pluginManager.ExecutePlugin(ctx, plugin.Name, pluginArgs, cluster)
        if err != nil {
            return NewTextResult("", fmt.Errorf("plugin execution failed: %w", err)), nil
        }
        
        return NewTextResult(result.Output, nil), nil
    }
}
```

### 4. Plugin Management Tools

```go
func (s *Server) createPluginManagementTools() []server.ServerTool {
    return []server.ServerTool{
        {Tool: mcp.NewTool("plugins_list",
            mcp.WithDescription("List all available kubectl plugins"),
            mcp.WithBoolean("show_details", mcp.Description("Include plugin details and usage statistics")),
            mcp.WithString("filter", mcp.Description("Filter plugins by name pattern")),
            mcp.WithTitleAnnotation("Plugins: List"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.pluginsList},
        
        {Tool: mcp.NewTool("plugins_discover",
            mcp.WithDescription("Discover and refresh available kubectl plugins"),
            mcp.WithBoolean("force", mcp.Description("Force re-discovery even if recently refreshed")),
            mcp.WithTitleAnnotation("Plugins: Discover"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.pluginsDiscover},
        
        {Tool: mcp.NewTool("plugins_info",
            mcp.WithDescription("Get detailed information about a specific plugin"),
            mcp.WithString("plugin", mcp.Description("Plugin name"), mcp.Required()),
            mcp.WithTitleAnnotation("Plugins: Info"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.pluginsInfo},
        
        {Tool: mcp.NewTool("plugins_install",
            mcp.WithDescription("Install a kubectl plugin via krew"),
            mcp.WithString("plugin", mcp.Description("Plugin name to install"), mcp.Required()),
            mcp.WithBoolean("update", mcp.Description("Update if already installed")),
            mcp.WithTitleAnnotation("Plugins: Install"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.pluginsInstall},
        
        {Tool: mcp.NewTool("plugins_execute",
            mcp.WithDescription("Execute a kubectl plugin with custom arguments"),
            mcp.WithString("plugin", mcp.Description("Plugin name"), mcp.Required()),
            mcp.WithArray("args", mcp.Description("Plugin arguments")),
            mcp.WithString("cluster", mcp.Description("Cluster to execute on")),
            mcp.WithTitleAnnotation("Plugins: Execute"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.pluginsExecute},
    }
}
```

### 5. Plugin Execution Engine

```go
// pkg/kubernetes/plugin_executor.go
type PluginResult struct {
    Output     string        `json:"output"`
    Error      string        `json:"error,omitempty"`
    ExitCode   int           `json:"exit_code"`
    Duration   time.Duration `json:"duration"`
    Plugin     string        `json:"plugin"`
    Cluster    string        `json:"cluster,omitempty"`
    Timestamp  time.Time     `json:"timestamp"`
}

func (pm *PluginManager) ExecutePlugin(ctx context.Context, pluginName string, args []string, cluster string) (*PluginResult, error) {
    start := time.Now()
    
    plugin, exists := pm.plugins[pluginName]
    if !exists {
        return nil, fmt.Errorf("plugin %s not found", pluginName)
    }
    
    // Set up execution context
    cmd := exec.CommandContext(ctx, plugin.Path, args...)
    
    // Set environment variables for cluster context
    env := os.Environ()
    if cluster != "" {
        // Set KUBECONFIG for specific cluster
        if kubeconfig, err := pm.getClusterKubeconfig(cluster); err == nil {
            env = append(env, fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
        }
    }
    cmd.Env = env
    
    // Execute plugin
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    
    err := cmd.Run()
    duration := time.Since(start)
    
    result := &PluginResult{
        Output:    stdout.String(),
        Error:     stderr.String(),
        Duration:  duration,
        Plugin:    pluginName,
        Cluster:   cluster,
        Timestamp: start,
    }
    
    if err != nil {
        if exitError, ok := err.(*exec.ExitError); ok {
            result.ExitCode = exitError.ExitCode()
        } else {
            result.ExitCode = -1
        }
    }
    
    // Update plugin usage statistics
    pm.mutex.Lock()
    plugin.LastUsed = time.Now()
    plugin.ExecutionCount++
    pm.mutex.Unlock()
    
    pm.logger.V(2).Info("Plugin executed", 
        "plugin", pluginName, 
        "cluster", cluster, 
        "duration", duration,
        "exit_code", result.ExitCode)
    
    return result, err
}
```

## Configuration Examples

### Basic Plugin Integration
```toml
# config.toml
[plugins]
enabled = true
auto_discovery = true
refresh_interval = "5m"
execution_timeout = "30s"
max_concurrent = 3
allow_any_plugin = false

# Specific plugins to allow
allowed_plugins = [
    "evict",
    "get_all", 
    "cnpg",
    "rook_ceph",
    "csi_scan"
]

# Plugins to block for security
blocked_plugins = [
    "dangerous_plugin",
    "legacy_tool"
]

# Additional plugin search paths
plugin_paths = [
    "/usr/local/bin",
    "~/.krew/bin",
    "~/go/bin"
]
```

### Environment-Specific Plugin Configuration
```toml
# Production environment - restrictive
[plugins]
enabled = true
auto_discovery = false
allow_any_plugin = false
execution_timeout = "60s"

allowed_plugins = [
    "get_all",
    "cnpg", 
    "rook_ceph"
]

# Cluster-specific plugins
[plugins.cluster_plugins]
"c1-sv5" = ["cnpg", "rook_ceph"]
"c1-dfw1" = ["get_all", "evict"]
```

```toml
# Development environment - permissive
[plugins]
enabled = true
auto_discovery = true
allow_any_plugin = true
execution_timeout = "5m"
refresh_interval = "1m"

blocked_plugins = [
    "kubectl-delete_everything"  # Safety first!
]
```

## Plugin Auto-Discovery Process

### 1. Discovery Algorithm
```go
func (pm *PluginManager) DiscoverPlugins() error {
    plugins := make(map[string]*PluginInfo)
    
    // Get plugins from kubectl plugin list
    kubectlPlugins, err := pm.getKubectlPlugins()
    if err != nil {
        pm.logger.Error(err, "Failed to get kubectl plugins")
    } else {
        for _, plugin := range kubectlPlugins {
            plugins[plugin.Name] = plugin
        }
    }
    
    // Scan additional plugin paths
    for _, path := range pm.config.PluginPaths {
        pathPlugins, err := pm.scanPluginPath(path)
        if err != nil {
            pm.logger.V(1).Info("Failed to scan plugin path", "path", path, "error", err)
            continue
        }
        
        for name, plugin := range pathPlugins {
            if existing, exists := plugins[name]; !exists || plugin.Path != existing.Path {
                plugins[name] = plugin
            }
        }
    }
    
    // Analyze plugin parameters (best effort)
    for _, plugin := range plugins {
        if params, err := pm.analyzePluginParameters(plugin); err == nil {
            plugin.Parameters = params
        }
    }
    
    pm.mutex.Lock()
    pm.plugins = plugins
    pm.mutex.Unlock()
    
    pm.logger.Info("Plugin discovery completed", "count", len(plugins))
    return nil
}

func (pm *PluginManager) analyzePluginParameters(plugin *PluginInfo) ([]PluginParameter, error) {
    // Run plugin with --help to extract parameters
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    cmd := exec.CommandContext(ctx, plugin.Path, "--help")
    output, err := cmd.CombinedOutput()
    if err != nil {
        return nil, err
    }
    
    return pm.parseHelpOutput(string(output))
}
```

### 2. Plugin Parameter Parsing
```go
func (pm *PluginManager) parseHelpOutput(helpText string) ([]PluginParameter, error) {
    var parameters []PluginParameter
    
    // Common patterns for extracting parameters from help text
    flagPattern := regexp.MustCompile(`(?m)^\s*-{1,2}([a-zA-Z0-9-_]+)(?:\s+([A-Z_]+))?\s+(.+)$`)
    matches := flagPattern.FindAllStringSubmatch(helpText, -1)
    
    for _, match := range matches {
        if len(match) >= 3 {
            param := PluginParameter{
                Name:        match[1],
                Description: strings.TrimSpace(match[3]),
                Type:        "string", // default
                Required:    false,    // default
            }
            
            // Infer type from help text
            if match[2] != "" {
                switch strings.ToLower(match[2]) {
                case "bool", "boolean":
                    param.Type = "bool"
                case "int", "number":
                    param.Type = "int"
                case "array", "list":
                    param.Type = "array"
                }
            }
            
            // Check if required
            if strings.Contains(strings.ToLower(param.Description), "required") {
                param.Required = true
            }
            
            parameters = append(parameters, param)
        }
    }
    
    return parameters, nil
}
```

## Security Model

### 1. Plugin Allowlisting
```go
func (pm *PluginManager) isPluginAllowed(pluginName string) bool {
    // Check blocked plugins first
    for _, blocked := range pm.config.BlockedPlugins {
        if matched, _ := filepath.Match(blocked, pluginName); matched {
            return false
        }
    }
    
    // If allow_any_plugin is true, allow unless blocked
    if pm.config.AllowAnyPlugin {
        return true
    }
    
    // Check allowed plugins list
    for _, allowed := range pm.config.AllowedPlugins {
        if matched, _ := filepath.Match(allowed, pluginName); matched {
            return true
        }
    }
    
    return false
}

func (pm *PluginManager) isPluginDestructive(pluginName string) bool {
    destructivePatterns := []string{
        "*delete*", "*remove*", "*destroy*", "*evict*", 
        "*drain*", "*cordon*", "*terminate*",
    }
    
    for _, pattern := range destructivePatterns {
        if matched, _ := filepath.Match(pattern, pluginName); matched {
            return true
        }
    }
    
    return false
}
```

### 2. Execution Limits
```go
type PluginExecutor struct {
    semaphore chan struct{} // Limit concurrent executions
    timeout   time.Duration
}

func (pe *PluginExecutor) Execute(ctx context.Context, plugin *PluginInfo, args []string) (*PluginResult, error) {
    // Acquire semaphore
    select {
    case pe.semaphore <- struct{}{}:
        defer func() { <-pe.semaphore }()
    case <-ctx.Done():
        return nil, ctx.Err()
    }
    
    // Apply timeout
    ctx, cancel := context.WithTimeout(ctx, pe.timeout)
    defer cancel()
    
    return pe.executeWithContext(ctx, plugin, args)
}
```

## Usage Examples

### AI Agent Interactions
```json
// Discover available plugins
{"method": "tools/call", "params": {"name": "plugins_list"}}

// Get plugin information
{"method": "tools/call", "params": {"name": "plugins_info", "arguments": {"plugin": "evict"}}}

// Execute plugin with parameters
{"method": "tools/call", "params": {"name": "plugin_evict", "arguments": {"cluster": "c1-sv5", "pod": "my-pod", "namespace": "default"}}}

// Install new plugin
{"method": "tools/call", "params": {"name": "plugins_install", "arguments": {"plugin": "neat"}}}

// Execute custom plugin command
{"method": "tools/call", "params": {"name": "plugins_execute", "arguments": {"plugin": "get_all", "args": ["--namespace", "kube-system"], "cluster": "c1-dfw1"}}}
```

### Command Line Usage
```bash
# Enable plugin support
kubernetes-mcp-server --config config.toml --plugins-enabled

# Custom plugin configuration
kubernetes-mcp-server \
  --plugins-enabled \
  --plugins-allowed "evict,get_all,cnpg" \
  --plugins-timeout 60s \
  --port 8080
```

## Benefits

### For AI Agents
- **Extended Capabilities**: Access to hundreds of kubectl plugins
- **Dynamic Discovery**: Automatically adapts to available plugins
- **Consistent Interface**: All plugins accessible via MCP tools
- **Parameter Validation**: Structured parameter handling

### For Users
- **Easy Integration**: No code changes needed to add plugins
- **Security Controls**: Fine-grained allowlisting and blocking
- **Performance**: Efficient execution with concurrency limits
- **Monitoring**: Usage statistics and execution tracking

### For Developers
- **Plugin Ecosystem**: Leverage existing kubectl plugin ecosystem
- **Automatic Wrapping**: Plugins become MCP tools automatically
- **Flexible Configuration**: Environment-specific plugin controls
- **Future-Proof**: New plugins automatically supported

## Implementation Timeline

### Phase 1: Core Plugin Infrastructure (Week 1)
- Plugin discovery mechanism
- Basic execution engine  
- Configuration schema
- Security model

### Phase 2: MCP Integration (Week 2)
- Dynamic tool generation
- Plugin management tools
- Parameter parsing and validation
- Multi-cluster plugin execution

### Phase 3: Advanced Features (Week 3)
- Krew integration for plugin installation
- Plugin usage statistics and monitoring
- Advanced parameter inference
- Plugin-specific optimizations

### Phase 4: Testing and Documentation (Week 4)
- Comprehensive testing with popular plugins
- Security testing and validation
- Performance optimization
- Documentation and examples

## Docker Integration

### Dockerfile with Netskope Artifactory

```dockerfile
ARG artifactory_endpoint=artifactory.netskope.io
FROM ${artifactory_endpoint}/pe-docker/ns-ubuntu-2004-fips:latest

# Update apt sources for development environment
RUN sed -i 's/artifactory-prod/artifactory-rd/' /etc/apt/sources.list.d/*.list

# Install required packages
RUN apt-get update && apt-get install -y \
    curl \
    wget \
    git \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install kubectl
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" \
    && chmod +x kubectl \
    && mv kubectl /usr/local/bin/

# Install krew (kubectl plugin manager)
RUN set -x; cd "$(mktemp -d)" && \
    OS="$(uname | tr '[:upper:]' '[:lower:]')" && \
    ARCH="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/\(arm\)\(64\)\?.*/\1\2/' -e 's/aarch64$/arm64/')" && \
    KREW="krew-${OS}_${ARCH}" && \
    curl -fsSLO "https://github.com/kubernetes-sigs/krew/releases/latest/download/${KREW}.tar.gz" && \
    tar zxvf "${KREW}.tar.gz" && \
    ./"${KREW}" install krew

# Add krew to PATH
ENV PATH="/root/.krew/bin:${PATH}"

# Install common kubectl plugins
RUN kubectl krew install \
    get-all \
    neat \
    ctx \
    ns \
    tree \
    who-can

# Copy application binary
COPY kubernetes-mcp-server /usr/local/bin/

# Create directories for configurations
RUN mkdir -p /etc/kubernetes/clusters \
    && mkdir -p /etc/mcp

# Set working directory
WORKDIR /app

# Default configuration
ENV PLUGINS_ENABLED=true
ENV PLUGINS_AUTO_DISCOVERY=true
ENV PLUGINS_REFRESH_INTERVAL=5m

# Expose MCP server port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Default command
CMD ["kubernetes-mcp-server", "--port", "8080", "--plugins-enabled"]
```

### Multi-Stage Build with Plugin Support

```dockerfile
ARG artifactory_endpoint=artifactory.netskope.io

# Build stage
FROM ${artifactory_endpoint}/pe-docker/golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o kubernetes-mcp-server ./cmd/kubernetes-mcp-server

# Runtime stage
FROM ${artifactory_endpoint}/pe-docker/ns-ubuntu-2004-fips:latest

# Update apt sources for development environment
RUN sed -i 's/artifactory-prod/artifactory-rd/' /etc/apt/sources.list.d/*.list

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    curl \
    wget \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

# Install kubectl
ARG KUBECTL_VERSION=v1.29.1
RUN curl -LO "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
    && chmod +x kubectl \
    && mv kubectl /usr/local/bin/

# Install krew
RUN set -x; cd "$(mktemp -d)" && \
    OS="$(uname | tr '[:upper:]' '[:lower:]')" && \
    ARCH="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/\(arm\)\(64\)\?.*/\1\2/' -e 's/aarch64$/arm64/')" && \
    KREW="krew-${OS}_${ARCH}" && \
    curl -fsSLO "https://github.com/kubernetes-sigs/krew/releases/latest/download/${KREW}.tar.gz" && \
    tar zxvf "${KREW}.tar.gz" && \
    ./"${KREW}" install krew

# Create non-root user
RUN groupadd -r mcp && useradd -r -g mcp mcp

# Set up directories
RUN mkdir -p /etc/kubernetes/clusters \
    && mkdir -p /etc/mcp \
    && mkdir -p /home/mcp/.krew \
    && chown -R mcp:mcp /home/mcp

# Copy binary from builder
COPY --from=builder /src/kubernetes-mcp-server /usr/local/bin/

# Add krew to PATH for mcp user
ENV PATH="/home/mcp/.krew/bin:${PATH}"

# Switch to non-root user
USER mcp
WORKDIR /home/mcp

# Install default plugins as mcp user
RUN kubectl krew install \
    get-all \
    neat \
    ctx \
    ns \
    tree

# Environment variables
ENV PLUGINS_ENABLED=true
ENV PLUGINS_AUTO_DISCOVERY=true
ENV PLUGINS_REFRESH_INTERVAL=5m
ENV PLUGINS_EXECUTION_TIMEOUT=30s

EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

CMD ["kubernetes-mcp-server", "--port", "8080", "--plugins-enabled", "--plugins-auto-discovery"]
```

### Docker Compose with Plugin Support

```yaml
# docker-compose-plugins.yml
version: '3.8'

services:
  mcp-production-plugins:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        artifactory_endpoint: artifactory.netskope.io
    image: ${artifactory_endpoint}/pe-docker/kubernetes-mcp-server:latest
    container_name: mcp-prod-plugins
    environment:
      - PROD_RANCHER_TOKEN=${PROD_RANCHER_TOKEN}
      - PLUGINS_ENABLED=true
      - PLUGINS_AUTO_DISCOVERY=true
      - PLUGINS_ALLOWED=get_all,cnpg,rook_ceph,neat
      - PLUGINS_EXECUTION_TIMEOUT=60s
      - PLUGINS_MAX_CONCURRENT=3
    volumes:
      - ./config-production.toml:/etc/mcp/config.toml:ro
      - ./clusters/prod:/etc/kubernetes/clusters/prod
      - production_krew_cache:/home/mcp/.krew
    ports:
      - "8080:8080"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
    restart: unless-stopped
    command: [
      "kubernetes-mcp-server", 
      "--config", "/etc/mcp/config.toml",
      "--port", "8080",
      "--plugins-enabled",
      "--plugins-auto-discovery"
    ]

  mcp-staging-plugins:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        artifactory_endpoint: artifactory.netskope.io
    image: ${artifactory_endpoint}/pe-docker/kubernetes-mcp-server:latest
    container_name: mcp-staging-plugins
    environment:
      - STAGING_RANCHER_TOKEN=${STAGING_RANCHER_TOKEN}
      - PLUGINS_ENABLED=true
      - PLUGINS_AUTO_DISCOVERY=true
      - PLUGINS_ALLOWED=get_all,neat,ctx,ns,tree
      - PLUGINS_EXECUTION_TIMEOUT=30s
    volumes:
      - ./config-staging.toml:/etc/mcp/config.toml:ro
      - ./clusters/staging:/etc/kubernetes/clusters/staging
      - staging_krew_cache:/home/mcp/.krew
    ports:
      - "8081:8080"
    restart: unless-stopped

volumes:
  production_krew_cache:
  staging_krew_cache:
```

### Kubernetes Deployment with Plugins

```yaml
# k8s-mcp-plugins-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kubernetes-mcp-server-plugins
  labels:
    app: kubernetes-mcp-server
    component: plugins
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kubernetes-mcp-server
      component: plugins
  template:
    metadata:
      labels:
        app: kubernetes-mcp-server
        component: plugins
    spec:
      containers:
      - name: mcp-server
        image: artifactory.netskope.io/pe-docker/kubernetes-mcp-server:latest
        args:
          - --config
          - /etc/mcp/config.toml
          - --port
          - "8080"
          - --plugins-enabled
          - --plugins-auto-discovery
        env:
        - name: PROD_RANCHER_TOKEN
          valueFrom:
            secretKeyRef:
              name: rancher-tokens
              key: production-token
        - name: PLUGINS_ENABLED
          value: "true"
        - name: PLUGINS_AUTO_DISCOVERY
          value: "true"
        - name: PLUGINS_ALLOWED
          value: "get_all,cnpg,rook_ceph,neat,ctx,ns"
        - name: PLUGINS_EXECUTION_TIMEOUT
          value: "60s"
        volumeMounts:
        - name: config
          mountPath: /etc/mcp
        - name: kubeconfigs
          mountPath: /etc/kubernetes/clusters
        - name: krew-cache
          mountPath: /home/mcp/.krew
        ports:
        - containerPort: 8080
          name: http
        livenessProbe:
          httpGet:
            path: /health
            port: http
          initialDelaySeconds: 30
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /health
            port: http
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
      volumes:
      - name: config
        configMap:
          name: mcp-config-plugins
      - name: kubeconfigs
        emptyDir: {}
      - name: krew-cache
        persistentVolumeClaim:
          claimName: krew-cache-pvc
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: krew-cache-pvc
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: mcp-config-plugins
data:
  config.toml: |
    [nsk]
    rancher_url = "https://rancher.netskope.io"
    rancher_token = "${PROD_RANCHER_TOKEN}"
    profile = "prod"
    config_dir = "/etc/kubernetes/clusters"
    auto_refresh = true
    refresh_interval = "1h"
    
    kubeconfig_dir = "/etc/kubernetes/clusters"
    default_cluster = "c1-sv5"
    auto_discovery = true
    
    [plugins]
    enabled = true
    auto_discovery = true
    refresh_interval = "5m"
    execution_timeout = "60s"
    max_concurrent = 3
    allow_any_plugin = false
    
    allowed_plugins = [
        "get_all",
        "cnpg", 
        "rook_ceph",
        "neat",
        "ctx",
        "ns"
    ]
    
    plugin_paths = [
        "/usr/local/bin",
        "/home/mcp/.krew/bin"
    ]
```

### Build and Deployment Scripts

```bash
#!/bin/bash
# build-with-plugins.sh

set -e

ARTIFACTORY_ENDPOINT="${ARTIFACTORY_ENDPOINT:-artifactory.netskope.io}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
IMAGE_NAME="${ARTIFACTORY_ENDPOINT}/pe-docker/kubernetes-mcp-server"

echo "Building Kubernetes MCP Server with Plugin Support..."
echo "Artifactory: ${ARTIFACTORY_ENDPOINT}"
echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"

# Build the Docker image
docker build \
  --build-arg artifactory_endpoint=${ARTIFACTORY_ENDPOINT} \
  -t ${IMAGE_NAME}:${IMAGE_TAG} \
  .

# Tag with version if provided
if [ -n "${VERSION}" ]; then
  docker tag ${IMAGE_NAME}:${IMAGE_TAG} ${IMAGE_NAME}:${VERSION}
  echo "Tagged as: ${IMAGE_NAME}:${VERSION}"
fi

echo "Build completed successfully!"

# Push to artifactory if PUSH=true
if [ "${PUSH}" = "true" ]; then
  echo "Pushing to artifactory..."
  docker push ${IMAGE_NAME}:${IMAGE_TAG}
  
  if [ -n "${VERSION}" ]; then
    docker push ${IMAGE_NAME}:${VERSION}
  fi
  
  echo "Push completed!"
fi
```

This integration transforms the Kubernetes MCP Server into a comprehensive kubectl plugin platform, providing AI agents with access to the entire ecosystem of kubectl extensions while maintaining security and performance standards.