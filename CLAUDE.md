# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a fork of the Kubernetes MCP Server project, extended to support Rancher-based multi-cluster environments. The goal is to enable AI agents to interact with multiple Kubernetes clusters by reading kubeconfig files from a directory and managing isolated client connections for each cluster.

### Key Technologies
- **Go 1.24.1** - Main programming language
- **Model Context Protocol (MCP)** - Protocol for AI agent interaction
- **Kubernetes client-go** - Official Kubernetes Go client library (target: v1.24.17 compatibility)
- **Helm v3** - Kubernetes package manager integration
- **mark3labs/mcp-go** - Go implementation of MCP protocol

### Version Requirements
- **Target Kubernetes Version**: v1.24.17
- **Note**: Current go.mod uses k8s.io/* v0.34.0 dependencies, but should be downgraded to match v1.24.17 cluster compatibility

### Rancher Integration Context
Rancher integration provides direct API access to download kubeconfig files for multiple clusters. Key concepts:
- Rancher API provides individual kubeconfig files per cluster
- Files are stored in a configured directory with names like `c1-am2.yaml`, `c1-sv5.yaml`
- Each cluster gets its own isolated client connection using its specific kubeconfig
- MCP tools: `rancher_list_clusters`, `rancher_download_cluster`, `rancher_download_all`

## Development Commands

### Build and Test
```bash
# Build the project
make build

# Build for all platforms
make build-all-platforms

# Run tests
make test

# Format code
make format

# Tidy dependencies
make tidy

# Lint code
make lint

# Clean build artifacts
make clean
```

### Development Workflow
```bash
# Full development cycle
make build && make test && make lint

# Run with mcp-inspector for debugging
make build
npx @modelcontextprotocol/inspector@latest $(pwd)/kubernetes-mcp-server
```

## Architecture

### Core Components

1. **Command Layer** (`cmd/kubernetes-mcp-server/`)
   - `main.go` - Entry point, sets up CLI flags and IOStreams
   - Delegates to root command in `pkg/kubernetes-mcp-server/cmd/root.go`

2. **MCP Server** (`pkg/mcp/`)
   - `mcp.go` - Core MCP server implementation and configuration
   - Tool handlers for Kubernetes operations (pods, resources, namespaces, etc.)
   - Profile system for different feature sets
   - Support for read-only and destructive operation controls

3. **Kubernetes Integration** (`pkg/kubernetes/`)
   - Direct API server interaction using client-go
   - Configuration loading and cluster connection management
   - Resource CRUD operations, pod exec, logs, metrics
   - Namespace and event handling
   - Access control and impersonation support

4. **Configuration** (`pkg/config/`)
   - Static configuration file support
   - Command-line flag integration
   - Environment variable handling

5. **HTTP Server** (`pkg/http/`)
   - SSE (Server-Sent Events) transport
   - OAuth/OIDC authentication support
   - Authorization middleware

6. **Output Formatting** (`pkg/output/`)
   - Table and YAML output formats
   - Configurable list formatting

### Key Features
- **Native Kubernetes API Integration** - Direct client-go usage, no kubectl subprocess calls
- **Helm Support** - Install, list, uninstall operations
- **Multiple Transport Modes** - STDIO, HTTP/SSE, OAuth-protected endpoints
- **Profile System** - Different tool sets (full, basic, read-only)
- **Access Control** - Read-only mode, destructive operation controls
- **Multi-cluster Ready** - Kubeconfig file specification support

## Planned Multi-Cluster Extension

### Target Architecture for Rancher Integration

The planned extension should:

1. **Directory-based Kubeconfig Loading**
   - Read multiple kubeconfig files from a specified directory
   - Maintain a mapping of cluster names to kubeconfig file paths
   - Add new MCP tools for cluster discovery and switching

2. **Cluster Context Management**
   - Add tools to list available clusters
   - Implement cluster switching via isolated client managers per cluster
   - Enable cross-cluster operations (run same command on all clusters)

3. **Enhanced MCP Tools**
   - `clusters_list` - List all available cluster configurations
   - `clusters_switch` - Switch active cluster context
   - `clusters_exec_all` - Execute operation across all clusters
   - Extend existing tools with cluster selection parameters

### Integration Points

Key files to modify for multi-cluster support:
- `pkg/kubernetes/configuration.go` - Extend kubeconfig loading logic
- `pkg/mcp/` - Add new cluster management tools
- `pkg/kubernetes-mcp-server/cmd/root.go` - Add kubeconfig directory flag
- Configuration system to support directory-based cluster discovery

### Rancher Integration Notes
- Rancher integration generates individual kubeconfig files per cluster
- File naming pattern: `{cluster-name}.yaml` (e.g., `c1-am2.yaml`)
- Supports multiple Rancher environments via configuration
- Periodic refresh of kubeconfig files for token rotation

## Configuration Patterns

### Command Line Flags
- `--kubeconfig` - Single kubeconfig file (current behavior)
- `--kubeconfig-dir` - Directory containing multiple kubeconfig files (planned)
- `--profile` - MCP profile selection (full, basic, read-only)
- `--read-only` - Restrict to read operations only
- `--port` - HTTP/SSE server mode

### Environment Variables
- Standard Kubernetes environment variables supported for cluster authentication
- Each cluster uses its own isolated kubeconfig file (no global KUBECONFIG dependency)

## Testing Strategy

- Unit tests use mock clients for Kubernetes API interactions
- Test files follow `*_test.go` naming convention
- Mock generation with `//go:generate` directives
- Integration tests can be run against real clusters
- Use `internal/test/mock_server.go` for MCP protocol testing

## Development Tips

- The project uses `klog/v2` for logging with verbosity levels 0-9
- Direct Kubernetes API access means no external kubectl dependency
- MCP protocol provides structured tool definitions with descriptions
- OAuth support enables secure multi-user deployments
- Profile system allows feature subsetting for different use cases

## Security Considerations

- Read-only mode available for production safety
- Destructive operations can be disabled
- OAuth/OIDC integration for authentication
- Token validation against Kubernetes API server
- Certificate authority validation support