# Docker Deployment Guide

This guide covers running the Kubernetes MCP Server in Docker with multi-cluster support.

## Quick Start

### Using docker-compose (Recommended)

The easiest way to run the server with all kubeconfig files mounted:

```bash
# Start the server
make compose-up

# View logs
make compose-logs

# Stop the server
make compose-down
```

### Using Docker directly

```bash
# Build the image
make docker-build

# Run with multi-cluster support
make docker-run

# View logs
make docker-logs

# Stop the container
make docker-stop
```

## Configuration

### Environment Variables

Create a `.env` file in the project root:

```env
# Rancher Integration (optional)
RANCHER_URL=https://rancher.example.com
RANCHER_TOKEN=your-rancher-api-token
RANCHER_CLUSTER_ID=optional-cluster-filter

# Logging
LOG_LEVEL=2

# Output format
LIST_OUTPUT=table
```

### Volume Mounts

The server needs access to your kubeconfig files. By default, `docker-compose.yml` mounts:

- `~/.mcp:/mcp:ro` - Kubeconfig directory (read-only)
- `~/.rancher:/rancher:ro` - Rancher config (optional, read-only)

### Port Configuration

Default port is `8080`. The server exposes:
- HTTP/MCP endpoint: `http://localhost:8080/mcp`
- SSE endpoint: `http://localhost:8080/sse`

## Makefile Commands

### Docker Commands

```bash
make docker-build          # Build Docker image
make docker-run            # Run container with multi-cluster support
make docker-run-rancher    # Run with Rancher integration
make docker-stop           # Stop and remove container
make docker-logs           # Show container logs
make docker-shell          # Open shell in running container
```

### Docker Compose Commands

```bash
make compose-up            # Start services
make compose-down          # Stop services
make compose-logs          # Show logs
make compose-rebuild       # Rebuild and restart
```

## Usage Examples

### Basic Multi-Cluster Setup

```bash
# Ensure kubeconfig files are in ~/.mcp/
ls ~/.mcp/*.yaml

# Start the server
make compose-up

# Test the server
curl http://localhost:8080/mcp
```

### With Rancher Integration

```bash
# Set environment variables
export RANCHER_URL=https://rancher.example.com
export RANCHER_TOKEN=your-token

# Run with Rancher support
make docker-run-rancher

# Or use docker-compose with .env file
make compose-up
```

### Custom Kubeconfig Directory

Edit `docker-compose.yml` to change the mount path:

```yaml
volumes:
  - /custom/path/to/configs:/mcp:ro
```

## Troubleshooting

### Container Won't Start

Check logs:
```bash
make docker-logs
```

Common issues:
- No kubeconfig files in `~/.mcp/`
- Permission issues with mounted volumes
- Port 8080 already in use

### Permission Errors

Ensure kubeconfig files are readable:
```bash
chmod 644 ~/.mcp/*.yaml
```

### Rancher Integration Not Working

Verify environment variables:
```bash
docker exec kubernetes-mcp-server env | grep RANCHER
```

### View Running Containers

```bash
docker ps | grep kubernetes-mcp-server
```

### Rebuild After Code Changes

```bash
make compose-rebuild
```

## Production Deployment

### Using Netskope Artifactory Images

The release process automatically builds and publishes Docker images to Netskope Artifactory:

```bash
docker pull artifactory.netskope.io/pe-sys-docker/kubernetes-mcp-server:v1.0.0
docker pull artifactory.netskope.io/pe-sys-docker/kubernetes-mcp-server:latest
```

Run the published image:

```bash
docker run -d \
  --name kubernetes-mcp-server \
  -p 8080:8080 \
  -v ~/.mcp:/mcp:ro \
  artifactory.netskope.io/pe-sys-docker/kubernetes-mcp-server:latest \
  --port=8080 \
  --kubeconfig-dir=/mcp
```

**Note:** You'll need to authenticate with Artifactory:
```bash
docker login artifactory.netskope.io
```

### Kubernetes Deployment

Example Deployment manifest:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kubernetes-mcp-server
  namespace: mcp
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kubernetes-mcp-server
  template:
    metadata:
      labels:
        app: kubernetes-mcp-server
    spec:
      containers:
      - name: kubernetes-mcp-server
        image: artifactory.netskope.io/pe-sys-docker/kubernetes-mcp-server:latest
        ports:
        - containerPort: 8080
          name: http
        volumeMounts:
        - name: kubeconfigs
          mountPath: /mcp
          readOnly: true
        env:
        - name: RANCHER_URL
          valueFrom:
            secretKeyRef:
              name: rancher-credentials
              key: url
        - name: RANCHER_TOKEN
          valueFrom:
            secretKeyRef:
              name: rancher-credentials
              key: token
        args:
        - "--port=8080"
        - "--kubeconfig-dir=/mcp"
        - "--log-level=2"
      volumes:
      - name: kubeconfigs
        configMap:
          name: kubeconfigs
---
apiVersion: v1
kind: Service
metadata:
  name: kubernetes-mcp-server
  namespace: mcp
spec:
  type: ClusterIP
  ports:
  - port: 8080
    targetPort: 8080
    protocol: TCP
    name: http
  selector:
    app: kubernetes-mcp-server
```

## Health Checks

The server exposes a `/healthz` endpoint for health checks:

```bash
# Check if server is listening
curl http://localhost:8080/healthz

# Check container health (using built-in HEALTHCHECK)
make docker-health
# Or directly:
docker inspect kubernetes-mcp-server --format='{{.State.Health.Status}}'

# View health check logs
docker inspect kubernetes-mcp-server --format='{{json .State.Health}}' | jq
```

## Security Considerations

1. **Read-only mounts**: Kubeconfig volumes are mounted read-only for security
2. **Non-root user**: Container runs as UID 1000 (non-root) for improved security
3. **Health checks**: Built-in HEALTHCHECK monitors container status
4. **Resource limits**: CPU and memory limits configured in docker-compose.yml
5. **Security options**: `no-new-privileges` prevents privilege escalation
6. **Secrets**: Use Docker secrets or environment variables for sensitive data
7. **Network isolation**: Use Docker networks to isolate the container
8. **Token rotation**: Rancher tokens should be rotated regularly

## Next Steps

- [Configuration Options](README.md#configuration)
- [Multi-Cluster Tools](README.md#multi-cluster-tools-netskope-fork)
- [Rancher Integration](README.md#rancher-integration-tools-netskope-fork)
