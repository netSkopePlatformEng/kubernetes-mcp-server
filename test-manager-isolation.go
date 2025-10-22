package main

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
)

func main() {
	logger := klog.Background()

	// Create config for multi-cluster
	cfg := &config.StaticConfig{
		KubeConfigDir: os.Getenv("HOME") + "/.mcp",
	}

	// Create multi-cluster manager
	mcm, err := kubernetes.NewMultiClusterManager(cfg, logger)
	if err != nil {
		panic(err)
	}

	// Start it
	ctx := context.Background()
	if err := mcm.Start(ctx); err != nil {
		panic(err)
	}

	fmt.Println("=== Testing Manager Isolation ===\n")

	// Test a few clusters
	clusters := []string{"local", "dp-iad0", "c1-prf-local"}

	for _, clusterName := range clusters {
		fmt.Printf("Cluster: %s\n", clusterName)

		// Get the manager for this cluster directly
		manager, err := mcm.GetManager(clusterName)
		if err != nil {
			fmt.Printf("  Error getting manager: %v\n\n", err)
			continue
		}

		// Get REST config
		restConfig, err := manager.ToRESTConfig()
		if err != nil {
			fmt.Printf("  Error getting REST config: %v\n\n", err)
			continue
		}

		fmt.Printf("  Server: %s\n", restConfig.Host)

		// Create a clientset and list nodes
		clientset, err := manager.ToDiscoveryClient()
		if err != nil {
			fmt.Printf("  Error getting clientset: %v\n\n", err)
			continue
		}

		// Get server version as a simple test
		version, err := clientset.ServerVersion()
		if err != nil {
			fmt.Printf("  Error getting version: %v\n\n", err)
			continue
		}
		fmt.Printf("  Version: %s\n", version.GitVersion)

		// Try to get actual nodes using dynamic client
		// (removed SetManager as it doesn't exist)

		// Create a derived instance
		derived, err := manager.Derived(ctx)
		if err != nil {
			fmt.Printf("  Error creating derived: %v\n\n", err)
			continue
		}

		// List nodes using ResourcesList
		gvk := &schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "Node",
		}

		nodes, err := derived.ResourcesList(ctx, gvk, "", kubernetes.ResourceListOptions{})
		if err != nil {
			fmt.Printf("  Error listing nodes: %v\n\n", err)
			continue
		}

		// Extract node names from the unstructured result
		fmt.Printf("  First node: (would need to parse unstructured data)\n\n")
		_ = nodes // We'd need to parse this unstructured data to get node names
	}

	// Now test switching
	fmt.Println("\n=== Testing Cluster Switching ===\n")

	for _, clusterName := range clusters {
		fmt.Printf("Switching to: %s\n", clusterName)

		// Switch cluster
		if err := mcm.SwitchCluster(clusterName); err != nil {
			fmt.Printf("  Error: %v\n\n", err)
			continue
		}

		// Verify active cluster
		active := mcm.GetActiveCluster()
		fmt.Printf("  Active cluster is now: %s\n", active)

		// Get active manager
		manager, err := mcm.GetActiveManager()
		if err != nil {
			fmt.Printf("  Error getting active manager: %v\n\n", err)
			continue
		}

		// Check its configuration
		restConfig, err := manager.ToRESTConfig()
		if err != nil {
			fmt.Printf("  Error getting REST config: %v\n\n", err)
			continue
		}

		fmt.Printf("  Active manager server: %s\n\n", restConfig.Host)
	}

	// Stop
	mcm.Stop()
}
