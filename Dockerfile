FROM golang:1.24 AS builder

WORKDIR /app

# Copy dependency files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY ./ ./

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux make build

# Production image
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

# OCI labels
LABEL org.opencontainers.image.title="Kubernetes MCP Server"
LABEL org.opencontainers.image.description="Kubernetes Model Context Protocol (MCP) server for AI agents with multi-cluster support"
LABEL org.opencontainers.image.vendor="Netskope"
LABEL org.opencontainers.image.url="https://github.com/netSkopePlatformEng/kubernetes-mcp-server"
LABEL org.opencontainers.image.source="https://github.com/netSkopePlatformEng/kubernetes-mcp-server"
LABEL org.opencontainers.image.licenses="Apache-2.0"

WORKDIR /app

# Create non-root user
RUN microdnf install -y shadow-utils && \
    useradd -r -u 1000 -g 0 -M -d /app -s /sbin/nologin mcp && \
    microdnf remove -y shadow-utils && \
    microdnf clean all

# Copy binary from builder
COPY --from=builder --chown=mcp:0 /app/kubernetes-mcp-server /app/kubernetes-mcp-server

# Create directory for kubeconfig files with proper permissions
RUN mkdir -p /mcp && \
    chown -R mcp:0 /mcp && \
    chmod -R g=u /mcp && \
    chmod -R 755 /mcp

# Switch to non-root user
USER 1000

# Expose MCP server port
EXPOSE 8080

# Health check
# The /healthz endpoint returns 200 OK when the server is running
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/kubernetes-mcp-server", "--help"] || exit 1

# Default to multi-cluster mode with kubeconfig directory
# Can be overridden with command-line args or environment variables
ENTRYPOINT ["/app/kubernetes-mcp-server"]
CMD ["--port", "8080", "--kubeconfig-dir", "/mcp"]
