# Testing the Kubernetes MCP Server

## Quick Start

### Build
```bash
make build
```

### Run Locally

#### STDIO Mode (Default)
```bash
./kubernetes-mcp-server
```

#### HTTP/SSE Mode
```bash
./kubernetes-mcp-server --port 8080
```

#### Multi-cluster Mode (NSK Integration)
```bash
./kubernetes-mcp-server --kubeconfig-dir ~/.mcp/
```

### Testing with MCP Inspector

#### Single Cluster Mode
```bash
npx @modelcontextprotocol/inspector@latest $(pwd)/kubernetes-mcp-server
```

#### Multi-cluster Mode (to see NSK tools)
```bash
npx @modelcontextprotocol/inspector@latest $(pwd)/kubernetes-mcp-server --kubeconfig-dir ~/.mcp
```

### Integration with Claude CLI

#### Add to Claude CLI
```bash
claude mcp add kubernetes-mcp-server $(pwd)/kubernetes-mcp-server
```

#### Add with Multi-cluster Support
```bash
# Use -- to separate claude mcp arguments from server arguments
claude mcp add kubernetes-mcp-server "$(pwd)/kubernetes-mcp-server" -- --kubeconfig-dir ~/.mcp
```

**Note**: The `--` is required to separate Claude CLI arguments from the MCP server arguments. Without it, `--kubeconfig-dir` would be interpreted as a Claude CLI option and cause an error.

#### List Available MCP Servers
```bash
claude mcp list
```

## Available Tools

### Single Cluster Mode
- `namespaces_list`, `pods_list`, `resources_list`
- `configuration_view`, `events_list`
- `helm_install`, `helm_list`, `helm_uninstall`

### Multi-cluster Mode (Additional NSK Tools)
- `clusters_list` - List all available clusters
- `clusters_switch` - Switch between clusters  
- `clusters_status` - Check cluster connectivity
- `clusters_exec_all` - Execute commands across all clusters
- `clusters_refresh` - Refresh cluster list

## Testing NSK Integration

Ensure you have NSK kubeconfig files in `~/.nsk/`:
```bash
ls ~/.nsk/*.yaml
```

Example cluster files:
- `c1-sv5.yaml`
- `c1-am2.yaml` 
- `dev-cluster.yaml`

## Troubleshooting

### Multi-cluster Tools Not Visible

If you don't see `clusters_list`, `clusters_switch`, etc. in Claude CLI after running `/mcp`:

1. **Check your MCP configuration**:
   ```bash
   claude mcp list
   ```
   Should show: `kubernetes-mcp-server: /path/to/kubernetes-mcp-server --kubeconfig-dir /path/to/configs`

2. **Verify kubeconfig files exist**:
   ```bash
   ls ~/.mcp/*.yaml
   ```

3. **Re-add with correct syntax**:
   ```bash
   claude mcp remove kubernetes-mcp-server -s local
   claude mcp add kubernetes-mcp-server "$(pwd)/kubernetes-mcp-server" -- --kubeconfig-dir ~/.mcp
   ```

4. **Test with inspector first**:
   ```bash
   npx @modelcontextprotocol/inspector@latest $(pwd)/kubernetes-mcp-server --kubeconfig-dir ~/.mcp
   ```

### Port Conflicts
Kill existing MCP inspector processes:
```bash
pkill -f "mcp.*inspector"
```

### Verify Build
```bash
./kubernetes-mcp-server --version
./kubernetes-mcp-server --help
```

### Run Tests
```bash
make test
go test ./pkg/nsk ./pkg/kubernetes ./pkg/mcp -v
```
