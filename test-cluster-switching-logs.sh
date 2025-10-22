#!/bin/bash

# Test cluster switching with logging
echo "Testing cluster switching with detailed logging..."

# Create a temporary log file
LOGFILE=$(mktemp)
echo "Logging to: $LOGFILE"

# Start the MCP server with logging
echo "Starting MCP server..."
./kubernetes-mcp-server --kubeconfig-dir ~/.mcp 2>&1 | tee "$LOGFILE" &
SERVER_PID=$!

# Give it time to start
sleep 3

# Use npx mcp-client-cli to interact with the server
echo -e "\n=== Switching to local cluster ==="
echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"clusters_switch","arguments":{"cluster":"local"}}}' | npx @modelcontextprotocol/cli 2>/dev/null | grep -o '"content".*'

echo -e "\n=== Listing nodes ==="
echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resources_list","arguments":{"apiVersion":"v1","kind":"Node"}}}' | npx @modelcontextprotocol/cli 2>/dev/null | head -10

# Wait a bit for logs to flush
sleep 2

# Kill the server
kill $SERVER_PID 2>/dev/null
wait $SERVER_PID 2>/dev/null

# Show relevant logs
echo -e "\n=== Relevant logs ==="
grep -E "(resourcesList|Switched cluster|Active cluster|server:)" "$LOGFILE" | tail -20

# Clean up
rm "$LOGFILE"

echo -e "\nTest complete!"