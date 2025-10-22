#!/bin/bash

# Start MCP server with verbose logging
echo "Starting MCP server with debug logging..."
./kubernetes-mcp-server --kubeconfig-dir ~/.mcp -v 2 2>&1 &
SERVER_PID=$!

# Give it time to start
sleep 3

# Test cluster switching
echo -e "\n=== Testing cluster switching ==="

# Switch to local
echo -e "\n1. Switching to local cluster..."
echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"clusters_switch","arguments":{"cluster":"local"}}}' | nc localhost 3000 2>/dev/null | grep -o '"content".*' || echo "Failed"

# Get nodes
echo -e "\n2. Getting nodes from local cluster..."
echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resources_list","arguments":{"apiVersion":"v1","kind":"Node"}}}' | nc localhost 3000 2>/dev/null | head -20 || echo "Failed"

# Stop server
echo -e "\nStopping server..."
kill $SERVER_PID 2>/dev/null
wait $SERVER_PID 2>/dev/null

echo "Test complete!"