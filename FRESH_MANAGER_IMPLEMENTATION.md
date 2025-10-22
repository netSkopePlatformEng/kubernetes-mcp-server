# Fresh Manager Implementation - kubectl-style Architecture

## Problem Solved
The cluster switching bug where the MCP server was returning nodes from the wrong cluster (Rancher management cluster IPs 198.18.29.x instead of expected local cluster IPs 10.136.2.x) has been fixed by implementing a fresh client approach similar to kubectl.

## Root Cause
The bug was caused by persistent state in the long-running MCP server process. When switching clusters, cached managers and clients were not being properly isolated, leading to state leakage between clusters.

## Solution: Fresh Manager Approach

### Key Implementation Files
1. **`pkg/kubernetes/multicluster_fresh.go`** - New FreshMultiClusterManager that creates fresh clients per operation
2. **`pkg/kubernetes/manager_simplified.go`** - SimplifiedManager using direct config building for Rancher compatibility
3. **`pkg/mcp/resources.go`** - Updated to use WithFreshManager pattern for resource operations
4. **`pkg/mcp/clusters.go`** - Updated cluster operations to use fresh managers

### Architecture Changes

#### Before (Problematic)
```
Long-running MCP Server
    └── Cached Manager (persistent)
        └── Shared clients across operations
            └── State persistence issues
```

#### After (kubectl-style)
```
Long-running MCP Server
    └── Fresh Manager per operation
        └── New clients created each time
            └── Clean slate, no state persistence
```

### Key Benefits
1. **No State Persistence**: Each operation gets a fresh manager, eliminating cross-cluster contamination
2. **kubectl Parity**: Mirrors kubectl's approach of creating fresh clients per invocation
3. **Proper Cleanup**: Managers are created and destroyed per operation, ensuring resource cleanup
4. **Rancher Compatibility**: SimplifiedManager uses `clientcmd.BuildConfigFromFlags()` which properly preserves Rancher proxy paths

### Usage Pattern
```go
// Instead of using a cached manager:
manager := s.clusterManager.GetActiveManager()
// ... use manager

// Use the fresh manager pattern:
err := s.clusterManager.WithFreshManager(func(manager *Manager) error {
    // Use manager for this operation
    // Manager is automatically cleaned up after
    return nil
})
```

### Test Results
The test program `test-fresh-switching.go` confirms:
- **Local cluster**: Correctly returns kmaster1.prime.iad0.netskope.com (10.136.2.34)
- **C1-prf-local cluster**: Correctly returns kmaster01.c1.perf.sje011.netskope.com (172.18.8.10)
- **Rapid switching**: No state leakage between clusters

### Performance Considerations
- Each operation creates a new manager (adds ~200ms latency)
- Trade-off: Slightly slower but guaranteed correctness
- Similar to kubectl's behavior - users expect this pattern

### Migration Notes
- FreshMultiClusterManager is a drop-in replacement for SimpleMultiClusterManager
- Existing code using the Kubernetes wrapper continues to work
- Only the underlying manager creation strategy changed

## Summary
By adopting kubectl's approach of creating fresh clients for each operation, we've eliminated the cluster switching bug while maintaining full compatibility with Rancher's proxy endpoints. The solution is clean, maintainable, and follows established Kubernetes patterns.