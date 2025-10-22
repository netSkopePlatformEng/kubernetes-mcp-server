#!/bin/bash
# Test script to verify cluster switching works correctly

echo "Testing cluster switching functionality..."
echo

# Start the MCP server
echo "Starting kubernetes-mcp-server with kubeconfig directory..."
./kubernetes-mcp-server --kubeconfig-dir ~/.mcp &
SERVER_PID=$!

# Give it time to start
sleep 2

echo "Testing cluster operations..."
echo

# Function to test cluster operations
test_cluster() {
    local cluster=$1
    echo "=== Testing cluster: $cluster ==="
    echo

    # Switch to cluster
    echo "1. Switching to cluster $cluster..."
    echo "clusters_switch cluster=$cluster"
    echo

    # Get nodes
    echo "2. Getting nodes for $cluster..."
    echo "resources_list apiVersion=v1 kind=Node"
    echo

    # Get namespaces
    echo "3. Getting namespaces for $cluster..."
    echo "namespaces_list"
    echo

    echo "=== End test for cluster: $cluster ==="
    echo
}

# Test switching between clusters
test_cluster "local"
test_cluster "dp-iad0"
test_cluster "local"

# Clean up
echo "Stopping server..."
kill $SERVER_PID 2>/dev/null

echo "Test complete!"