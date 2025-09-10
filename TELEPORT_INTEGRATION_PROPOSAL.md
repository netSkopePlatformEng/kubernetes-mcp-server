# Teleport Integration for Kubernetes MCP Server

## Executive Summary

This proposal outlines the integration of Teleport access management with the Kubernetes MCP Server, enabling secure, audited, and identity-aware access to multiple Kubernetes clusters across different environments (Commercial, FedRAMP, PBMM). The integration leverages Teleport's authentication, authorization, and session management capabilities to provide enterprise-grade security for AI agent interactions.

## Current Teleport Environment Analysis

Based on system analysis, the environment includes:

### **Multiple Teleport Clusters:**
- **Commercial**: `teleport.netskope.io:443` (iad0 cluster)
- **FedRAMP Production**: `teleport.govskope.io:443` (fedramp-prod cluster)  
- **FedRAMP Preprod**: `teleport.betagovskope.io:443` (fedhigh-preprod cluster)
- **PBMM Production**: `teleport.cagovskope.io:443` (pbmm cluster)
- **PBMM Preprod**: `teleport-yyz2.cagovskope.io:3080` (pbmm-prod cluster)

### **Role-Based Access:**
- **Extensive POP Roles**: Access to 80+ Point-of-Presence locations worldwide
- **Kubernetes Admin Roles**: `k8s-npe-admin`, `k8s-prod-admin`, `teleport-k8s-*-admin`
- **Environment-Specific Roles**: Commercial, FedRAMP, PBMM isolation
- **Application Access**: Role-based application access controls

### **Kubernetes Integration:**
- **Kubernetes Groups**: `admins`, `teleport-admins`, `admin`
- **Multi-Environment**: Different Kubernetes access per Teleport cluster
- **Session Management**: Time-limited access with automatic expiration

## Proposed Architecture

### 1. Teleport Authentication Manager

```go
// pkg/teleport/auth_manager.go
type TeleportAuthManager struct {
    profiles        map[string]*TeleportProfile
    activeProfile   string
    tshPath         string
    autoRenew       bool
    renewThreshold  time.Duration
    mutex           sync.RWMutex
    logger          klog.Logger
}

type TeleportProfile struct {
    Name              string            `json:"name"`
    ProxyURL          string            `json:"proxy_url"`
    Cluster           string            `json:"cluster"`
    User              string            `json:"user"`
    Roles             []string          `json:"roles"`
    KubernetesEnabled bool              `json:"kubernetes_enabled"`
    KubernetesGroups  []string          `json:"kubernetes_groups"`
    ValidUntil        time.Time         `json:"valid_until"`
    IsExpired         bool              `json:"is_expired"`
    Environment       string            `json:"environment"` // Commercial, FedRAMP, PBMM
}

func NewTeleportAuthManager(config *TeleportConfig) *TeleportAuthManager
func (tam *TeleportAuthManager) LoadProfiles() error
func (tam *TeleportAuthManager) GetActiveProfile() (*TeleportProfile, error)
func (tam *TeleportAuthManager) SwitchProfile(profileName string) error
func (tam *TeleportAuthManager) RefreshCredentials(profileName string) error
func (tam *TeleportAuthManager) GetKubernetesAccess(profileName string) (*KubernetesAccess, error)
func (tam *TeleportAuthManager) ListAvailableClusters(profileName string) ([]string, error)
```

### 2. Enhanced Configuration Schema

```go
// pkg/config/config.go additions
type StaticConfig struct {
    // ... existing fields
    
    // Teleport Integration
    TeleportIntegration *TeleportConfig `toml:"teleport,omitempty"`
}

type TeleportConfig struct {
    // Core Teleport settings
    Enabled           bool     `toml:"enabled,omitempty"`
    TSHPath           string   `toml:"tsh_path,omitempty"` // default: "tsh"
    DefaultProfile    string   `toml:"default_profile,omitempty"`
    AutoRenew         bool     `toml:"auto_renew,omitempty"`
    RenewThreshold    string   `toml:"renew_threshold,omitempty"` // e.g., "1h"
    
    // Multi-environment settings
    Environments      map[string]*TeleportEnvironment `toml:"environments,omitempty"`
    
    // Session management
    SessionTimeout    string   `toml:"session_timeout,omitempty"`
    RequireValidAuth  bool     `toml:"require_valid_auth,omitempty"`
    
    // Kubernetes integration
    KubernetesAccess  *TeleportKubernetesConfig `toml:"kubernetes,omitempty"`
}

type TeleportEnvironment struct {
    ProxyURL          string   `toml:"proxy_url"`
    AllowedClusters   []string `toml:"allowed_clusters,omitempty"`
    RequiredRoles     []string `toml:"required_roles,omitempty"`
    KubernetesEnabled bool     `toml:"kubernetes_enabled,omitempty"`
}

type TeleportKubernetesConfig struct {
    AutoDiscovery     bool     `toml:"auto_discovery,omitempty"`
    ClusterMapping    map[string]string `toml:"cluster_mapping,omitempty"`
    RequiredGroups    []string `toml:"required_groups,omitempty"`
}
```

### 3. Teleport-Aware Cluster Management

```go
// pkg/kubernetes/teleport_cluster_manager.go
type TeleportClusterManager struct {
    *ClusterManager
    teleportAuth     *teleport.TeleportAuthManager
    profileClusters  map[string][]string // profile -> clusters
    activeSession    *TeleportSession
}

type TeleportSession struct {
    Profile        string
    KubeContext    string
    ValidUntil     time.Time
    KubernetesAccess bool
    Groups         []string
}

func NewTeleportClusterManager(teleportAuth *teleport.TeleportAuthManager, config *config.StaticConfig) *TeleportClusterManager
func (tcm *TeleportClusterManager) DiscoverClustersFromTeleport() error
func (tcm *TeleportClusterManager) GetClustersForProfile(profile string) ([]string, error)
func (tcm *TeleportClusterManager) SwitchToTeleportCluster(profile, cluster string) error
func (tcm *TeleportClusterManager) ValidateAccess(profile, cluster string) error
```

### 4. New MCP Tools for Teleport Management

```go
// pkg/mcp/teleport.go
func (s *Server) initTeleportTools() []server.ServerTool {
    if s.teleportAuth == nil {
        return nil
    }
    
    return []server.ServerTool{
        {Tool: mcp.NewTool("teleport_profiles_list",
            mcp.WithDescription("List all available Teleport profiles and their status"),
            mcp.WithBoolean("show_expired", mcp.Description("Include expired profiles in the list")),
            mcp.WithTitleAnnotation("Teleport: List Profiles"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.teleportProfilesList},
        
        {Tool: mcp.NewTool("teleport_profile_switch",
            mcp.WithDescription("Switch to a different Teleport profile"),
            mcp.WithString("profile", mcp.Description("Teleport profile name to switch to"), mcp.Required()),
            mcp.WithTitleAnnotation("Teleport: Switch Profile"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.teleportProfileSwitch},
        
        {Tool: mcp.NewTool("teleport_status",
            mcp.WithDescription("Get current Teleport authentication status and session information"),
            mcp.WithString("profile", mcp.Description("Specific profile to check (optional, checks all if not provided)")),
            mcp.WithTitleAnnotation("Teleport: Authentication Status"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.teleportStatus},
        
        {Tool: mcp.NewTool("teleport_login",
            mcp.WithDescription("Authenticate with Teleport using specified proxy"),
            mcp.WithString("proxy", mcp.Description("Teleport proxy URL"), mcp.Required()),
            mcp.WithString("user", mcp.Description("Username for authentication")),
            mcp.WithBoolean("browser", mcp.Description("Use browser-based authentication")),
            mcp.WithTitleAnnotation("Teleport: Login"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.teleportLogin},
        
        {Tool: mcp.NewTool("teleport_logout",
            mcp.WithDescription("Logout from Teleport profile"),
            mcp.WithString("profile", mcp.Description("Profile to logout from (optional, logs out all if not provided)")),
            mcp.WithTitleAnnotation("Teleport: Logout"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.teleportLogout},
        
        {Tool: mcp.NewTool("teleport_kube_clusters",
            mcp.WithDescription("List Kubernetes clusters available through Teleport"),
            mcp.WithString("profile", mcp.Description("Teleport profile to check (optional, uses active profile)")),
            mcp.WithString("environment", mcp.Description("Filter by environment (Commercial, FedRAMP, PBMM)")),
            mcp.WithTitleAnnotation("Teleport: Kubernetes Clusters"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.teleportKubeClusters},
        
        {Tool: mcp.NewTool("teleport_kube_login",
            mcp.WithDescription("Login to a Kubernetes cluster through Teleport"),
            mcp.WithString("cluster", mcp.Description("Kubernetes cluster name"), mcp.Required()),
            mcp.WithString("profile", mcp.Description("Teleport profile to use (optional, uses active profile)")),
            mcp.WithTitleAnnotation("Teleport: Kubernetes Login"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.teleportKubeLogin},
        
        {Tool: mcp.NewTool("teleport_session_info",
            mcp.WithDescription("Get detailed information about current Teleport session"),
            mcp.WithString("profile", mcp.Description("Specific profile to inspect")),
            mcp.WithTitleAnnotation("Teleport: Session Information"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.teleportSessionInfo},
        
        {Tool: mcp.NewTool("teleport_renew",
            mcp.WithDescription("Renew Teleport credentials for specified profile"),
            mcp.WithString("profile", mcp.Description("Profile to renew (optional, renews active profile)")),
            mcp.WithTitleAnnotation("Teleport: Renew Credentials"),
            mcp.WithReadOnlyHintAnnotation(false),
        ), Handler: s.teleportRenew},
    }
}
```

### 5. Enhanced Kubernetes Tools with Teleport Awareness

```go
// All existing Kubernetes tools get enhanced with Teleport context
{Tool: mcp.NewTool("pods_list",
    mcp.WithDescription("List all Kubernetes pods in the current cluster from all namespaces"),
    mcp.WithString("teleport_profile", mcp.Description("Teleport profile to use for authentication (optional)")),
    mcp.WithString("cluster", mcp.Description("Cluster name (optional, uses active cluster if not specified)")),
    mcp.WithString("labelSelector", mcp.Description("Optional Kubernetes label selector")),
    // ...existing parameters
), Handler: s.podsListWithTeleport},
```

### 6. Teleport Command Execution Engine

```go
// pkg/teleport/executor.go
type TeleportExecutor struct {
    authManager    *TeleportAuthManager
    tshPath        string
    sessionTimeout time.Duration
}

func (te *TeleportExecutor) ExecuteWithProfile(ctx context.Context, profile string, args []string) (*TeleportResult, error) {
    // Validate profile authentication
    if err := te.validateProfileAuth(profile); err != nil {
        return nil, fmt.Errorf("profile authentication failed: %w", err)
    }
    
    // Execute tsh command with profile context
    cmd := exec.CommandContext(ctx, te.tshPath, append([]string{"--proxy", profile}, args...)...)
    
    // Set up environment and execute
    result, err := te.executeCommand(cmd)
    if err != nil && te.isAuthError(err) {
        // Attempt to refresh credentials
        if refreshErr := te.authManager.RefreshCredentials(profile); refreshErr == nil {
            // Retry once after refresh
            result, err = te.executeCommand(cmd)
        }
    }
    
    return result, err
}

func (te *TeleportExecutor) GetKubernetesConfig(profile, cluster string) (string, error) {
    args := []string{"kube", "login", cluster, "--format", "kubeconfig"}
    result, err := te.ExecuteWithProfile(context.Background(), profile, args)
    if err != nil {
        return "", err
    }
    return result.Output, nil
}
```

## Configuration Examples

### Multi-Environment Teleport Setup

```toml
# config-teleport.toml
[teleport]
enabled = true
tsh_path = "tsh"
default_profile = "commercial"
auto_renew = true
renew_threshold = "2h"
session_timeout = "8h"
require_valid_auth = true

[teleport.environments]
[teleport.environments.commercial]
proxy_url = "teleport.netskope.io:443"
allowed_clusters = ["iad0"]
required_roles = ["k8s-npe-admin", "k8s-prod-admin"]
kubernetes_enabled = true

[teleport.environments.fedramp-prod]
proxy_url = "teleport.govskope.io:443"
allowed_clusters = ["fedramp-prod"]
required_roles = ["fedh-teleport-k8s-prod-ncd-admin"]
kubernetes_enabled = true

[teleport.environments.fedramp-preprod]
proxy_url = "teleport.betagovskope.io:443"
allowed_clusters = ["fedhigh-preprod"]
required_roles = ["fedh-teleport-k8s-preprod-ncd-admin"]
kubernetes_enabled = true

[teleport.environments.pbmm]
proxy_url = "teleport.cagovskope.io:443"
allowed_clusters = ["pbmm"]
required_roles = ["fedh-teleport-k8s-prod-ncd-admin"]
kubernetes_enabled = true

[teleport.kubernetes]
auto_discovery = true
required_groups = ["admins", "teleport-admins"]

# Multi-cluster with Teleport integration
kubeconfig_dir = "/tmp/teleport-clusters"
auto_discovery = true
default_cluster = "auto"

# NSK integration can work alongside Teleport
[nsk]
# NSK for non-Teleport managed clusters
enabled = false
```

### Environment-Specific Configurations

#### Commercial Environment
```toml
# config-commercial.toml
[teleport]
enabled = true
default_profile = "commercial"
environments.commercial.proxy_url = "teleport.netskope.io:443"
environments.commercial.kubernetes_enabled = true
```

#### FedRAMP Environment
```toml
# config-fedramp.toml
[teleport]
enabled = true
default_profile = "fedramp-prod"
require_valid_auth = true
session_timeout = "4h"

[teleport.environments.fedramp-prod]
proxy_url = "teleport.govskope.io:443"
required_roles = ["fedh-teleport-k8s-prod-ncd-admin"]
kubernetes_enabled = true
```

## Docker Integration with Teleport

### Enhanced Dockerfile with Teleport Support

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

# Install Teleport tsh client
ARG TELEPORT_VERSION=15.4.29
RUN curl -L -O https://cdn.teleport.dev/teleport-v${TELEPORT_VERSION}-linux-amd64-bin.tar.gz \
    && tar -xzf teleport-v${TELEPORT_VERSION}-linux-amd64-bin.tar.gz \
    && install teleport/tsh /usr/local/bin/ \
    && rm -rf teleport teleport-v${TELEPORT_VERSION}-linux-amd64-bin.tar.gz

# Install kubectl
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" \
    && chmod +x kubectl \
    && mv kubectl /usr/local/bin/

# Copy application binary
COPY kubernetes-mcp-server /usr/local/bin/

# Create directories for configurations
RUN mkdir -p /etc/kubernetes/clusters \
    && mkdir -p /etc/mcp \
    && mkdir -p /home/mcp/.tsh

# Create non-root user
RUN groupadd -r mcp && useradd -r -g mcp mcp \
    && chown -R mcp:mcp /home/mcp

# Switch to non-root user
USER mcp
WORKDIR /home/mcp

# Environment variables
ENV TELEPORT_ENABLED=true
ENV TELEPORT_AUTO_RENEW=true
ENV TELEPORT_SESSION_TIMEOUT=8h

EXPOSE 8080

CMD ["kubernetes-mcp-server", "--port", "8080", "--teleport-enabled"]
```

### Docker Compose with Teleport

```yaml
# docker-compose-teleport.yml
version: '3.8'

services:
  mcp-commercial-teleport:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        artifactory_endpoint: artifactory.netskope.io
    image: ${artifactory_endpoint}/pe-docker/kubernetes-mcp-server:latest
    container_name: mcp-commercial-teleport
    environment:
      - TELEPORT_ENABLED=true
      - TELEPORT_DEFAULT_PROFILE=commercial
      - TELEPORT_AUTO_RENEW=true
      - TELEPORT_PROXY_URL=teleport.netskope.io:443
    volumes:
      - ./config-commercial.toml:/etc/mcp/config.toml:ro
      - teleport_commercial_cache:/home/mcp/.tsh
    ports:
      - "8080:8080"
    restart: unless-stopped

  mcp-fedramp-teleport:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        artifactory_endpoint: artifactory.netskope.io
    image: ${artifactory_endpoint}/pe-docker/kubernetes-mcp-server:latest
    container_name: mcp-fedramp-teleport
    environment:
      - TELEPORT_ENABLED=true
      - TELEPORT_DEFAULT_PROFILE=fedramp-prod
      - TELEPORT_REQUIRE_VALID_AUTH=true
      - TELEPORT_PROXY_URL=teleport.govskope.io:443
    volumes:
      - ./config-fedramp.toml:/etc/mcp/config.toml:ro
      - teleport_fedramp_cache:/home/mcp/.tsh
    ports:
      - "8081:8080"
    restart: unless-stopped

volumes:
  teleport_commercial_cache:
  teleport_fedramp_cache:
```

## Security Model

### 1. Authentication Flow
```go
func (tam *TeleportAuthManager) ValidateAndRefreshAuth(profile string) error {
    p, exists := tam.profiles[profile]
    if !exists {
        return fmt.Errorf("profile %s not found", profile)
    }
    
    // Check if credentials are expired or near expiration
    if time.Until(p.ValidUntil) < tam.renewThreshold {
        return tam.RefreshCredentials(profile)
    }
    
    return nil
}

func (tam *TeleportAuthManager) RefreshCredentials(profile string) error {
    cmd := exec.Command(tam.tshPath, "login", "--proxy", profile)
    return cmd.Run()
}
```

### 2. Role-Based Access Control
```go
func (tam *TeleportAuthManager) ValidateKubernetesAccess(profile, cluster string) error {
    p, exists := tam.profiles[profile]
    if !exists {
        return fmt.Errorf("profile %s not found", profile)
    }
    
    if !p.KubernetesEnabled {
        return fmt.Errorf("Kubernetes access not enabled for profile %s", profile)
    }
    
    // Check required roles
    if !tam.hasRequiredRoles(p.Roles) {
        return fmt.Errorf("insufficient roles for Kubernetes access")
    }
    
    return nil
}
```

### 3. Session Management
```go
type TeleportSessionManager struct {
    sessions    map[string]*TeleportSession
    maxSessions int
    timeout     time.Duration
}

func (tsm *TeleportSessionManager) CreateSession(profile, cluster string) (*TeleportSession, error) {
    session := &TeleportSession{
        Profile:     profile,
        KubeContext: cluster,
        ValidUntil:  time.Now().Add(tsm.timeout),
        CreatedAt:   time.Now(),
    }
    
    tsm.sessions[session.ID] = session
    return session, nil
}
```

## Usage Examples

### AI Agent Interactions

```json
// Check Teleport authentication status
{"method": "tools/call", "params": {"name": "teleport_status"}}

// Switch to FedRAMP environment
{"method": "tools/call", "params": {"name": "teleport_profile_switch", "arguments": {"profile": "fedramp-prod"}}}

// List Kubernetes clusters in FedRAMP
{"method": "tools/call", "params": {"name": "teleport_kube_clusters", "arguments": {"profile": "fedramp-prod"}}}

// Login to specific Kubernetes cluster
{"method": "tools/call", "params": {"name": "teleport_kube_login", "arguments": {"cluster": "fedramp-prod", "profile": "fedramp-prod"}}}

// List pods with Teleport authentication
{"method": "tools/call", "params": {"name": "pods_list", "arguments": {"teleport_profile": "fedramp-prod"}}}

// Cross-environment cluster operations
{"method": "tools/call", "params": {"name": "teleport_profiles_list", "arguments": {"show_expired": false}}}
```

### Command Line Usage

```bash
# Start MCP server with Teleport integration
kubernetes-mcp-server \
  --teleport-enabled \
  --teleport-default-profile commercial \
  --teleport-auto-renew \
  --port 8080

# Environment-specific startup
kubernetes-mcp-server \
  --config config-fedramp.toml \
  --teleport-require-valid-auth \
  --port 8081
```

## Benefits

### For Security Teams
- **Centralized Authentication**: All Kubernetes access goes through Teleport
- **Session Auditing**: Complete audit trail of AI agent actions
- **Role-Based Access**: Granular permissions based on Teleport roles
- **Environment Isolation**: Strict separation between Commercial, FedRAMP, PBMM

### For AI Agents
- **Seamless Access**: Transparent authentication handling
- **Multi-Environment**: Access to all authorized environments
- **Session Management**: Automatic credential renewal
- **Context Awareness**: Environment-specific operations

### For Platform Engineers
- **Unified Access**: Single interface for all Kubernetes environments
- **Compliance Ready**: FedRAMP and PBMM compliant access patterns
- **Operational Efficiency**: Reduced manual authentication steps
- **Monitoring**: Integration with existing Teleport monitoring

### For Developers
- **Familiar Patterns**: Uses existing Teleport workflows
- **Flexible Configuration**: Environment-specific deployments
- **Easy Integration**: Minimal changes to existing MCP usage
- **Future-Proof**: Leverages Teleport's evolution

## Implementation Timeline

### Phase 1: Core Teleport Integration (Week 1-2)
- Teleport authentication manager
- Profile discovery and management
- Basic tsh command execution
- Configuration schema updates

### Phase 2: MCP Tools Implementation (Week 3)
- Teleport management tools
- Enhanced Kubernetes tools with Teleport awareness
- Session management and renewal
- Error handling and retry logic

### Phase 3: Advanced Features (Week 4)
- Multi-environment support
- Kubernetes cluster discovery via Teleport
- Docker integration with Teleport client
- Comprehensive authentication flows

### Phase 4: Testing and Compliance (Week 5)
- Security testing across all environments
- FedRAMP and PBMM compliance validation
- Performance optimization
- Documentation and deployment guides

## Compliance and Audit

### FedRAMP Requirements
- All access through approved Teleport instances
- Session logging and audit trails
- Multi-factor authentication enforcement
- Regular credential rotation

### PBMM Requirements
- Canadian data sovereignty compliance
- Government-approved authentication flows
- Classified data access controls
- Audit trail preservation

### Commercial Requirements
- Corporate identity integration
- Standard security policies
- Operational monitoring
- Performance optimization

This Teleport integration transforms the Kubernetes MCP Server into an enterprise-ready platform that provides secure, audited, and compliant access to multiple Kubernetes environments while maintaining the flexibility and power of the MCP protocol for AI agent interactions.