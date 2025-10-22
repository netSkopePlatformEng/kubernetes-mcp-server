# Cluster Switching Bug Analysis

## Executive Summary
The kubernetes-mcp-server has a critical bug where cluster switching does not work correctly when using Rancher-proxied kubeconfigs. All clusters return nodes from the Rancher management cluster instead of the intended downstream clusters.

## Problem Description

### Expected Behavior
- Switching to "local" cluster should return nodes from prime.iad0.netskope.com domain with IPs in 10.136.2.x range
- Switching to "c1-prf-local" should return nodes from c1.perf.sje011.netskope.com with IPs in 172.18.8.x range

### Actual Behavior
- All clusters return the same nodes with short hostnames (kmaster01, knode01) and IPs in 198.18.29.x range
- These nodes are from the Rancher management cluster itself, not the downstream clusters

## Root Cause Analysis

### Working Correctly: kubectl
```bash
# kubectl with local.yaml correctly returns:
kmaster1.prime.iad0.netskope.com   10.136.2.34
kmaster2.prime.iad0.netskope.com   10.136.2.35

# kubectl with c1-prf-local.yaml correctly returns:
kmaster01.c1.perf.sje011.netskope.com   172.18.8.10
kmaster03.c1.perf.sje011.netskope.com   172.18.8.16
```

### Not Working: MCP Server
Both clusters return the same nodes:
```
kmaster01   198.18.29.86
kmaster02   198.18.29.73
kmaster03   198.18.29.64
```

## Technical Details

### Rancher Proxy Configuration
The kubeconfigs use Rancher proxy endpoints:
- `local`: `https://rancher.prime.iad0.netskope.com/k8s/clusters/local`
- `c1-prf-local`: `https://rancher.prime.iad0.netskope.com/k8s/clusters/c-gk4kg`

Each kubeconfig has a cluster-specific bearer token that should route to the correct downstream cluster.

### Code Investigation
1. **SimpleMultiClusterManager**: Correctly switches between kubeconfig files and creates new Manager instances
2. **Manager Creation**: Properly loads the kubeconfig with the correct path and server endpoint
3. **REST Client**: This is where the issue likely resides - the client-go library may be caching or incorrectly handling the Rancher proxy endpoints

## Potential Solutions

### Solution 1: Force Complete Client Refresh
Ensure that when switching clusters, ALL client-go components are recreated:
- Clear any global caches in client-go
- Create completely new REST configs without any shared state
- Ensure discovery client caches are invalidated

### Solution 2: Direct Cluster Access
Instead of using Rancher proxy endpoints, configure direct access to clusters:
- Download kubeconfigs with direct cluster endpoints
- Bypass Rancher proxy layer entirely
- This would require network access to the actual cluster API servers

### Solution 3: Fix REST Client Configuration
The issue may be in how the REST client handles the path-based routing:
- Ensure the full path (`/k8s/clusters/{id}/`) is preserved in API calls
- Check if client-go is stripping the path prefix
- Verify bearer token is being sent correctly for each cluster

## Immediate Workaround
For testing purposes, you can:
1. Use kubectl directly with the kubeconfig files (works correctly)
2. Use a local development cluster (like colima) instead of Rancher-proxied clusters
3. Configure direct cluster access if network allows

## Next Steps
1. Add detailed HTTP request logging to see exact API calls being made
2. Compare HTTP requests between kubectl (working) and MCP server (not working)
3. Investigate client-go caching mechanisms
4. Test with non-Rancher clusters to isolate the issue

## Code Changes Made
- Created `SimpleMultiClusterManager` for cleaner cluster switching
- Added proper resource cleanup when switching clusters
- Ensured fresh Manager instances are created for each cluster
- Added debug logging throughout the switching process

Despite these improvements, the underlying issue persists and appears to be related to how client-go handles Rancher's proxy endpoints.