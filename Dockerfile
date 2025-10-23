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

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/kubernetes-mcp-server /app/kubernetes-mcp-server

# Create directory for kubeconfig files
RUN mkdir -p /mcp && chmod 755 /mcp

# Expose MCP server port
EXPOSE 8080

# Default to multi-cluster mode with kubeconfig directory
# Can be overridden with command-line args or environment variables
ENTRYPOINT ["/app/kubernetes-mcp-server"]
CMD ["--port", "8080", "--kubeconfig-dir", "/mcp"]
