package main

import (
	"context"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

	// Create simplified multi-cluster manager
	mcm, err := kubernetes.NewSimpleMultiClusterManager(cfg, logger)
	if err != nil {
		panic(err)
	}

	// Start it
	ctx := context.Background()
	if err := mcm.Start(ctx); err != nil {
		panic(err)
	}
	defer mcm.Stop()

	fmt.Println("=== Testing Simplified Manager Cluster Switching ===\n")

	// Test clusters
	clusters := []string{"local", "c1-prf-local"}

	for _, clusterName := range clusters {
		fmt.Printf("Switching to cluster: %s\n", clusterName)

		// Switch cluster
		if err := mcm.SwitchCluster(clusterName); err != nil {
			fmt.Printf("  Error switching: %v\n\n", err)
			continue
		}

		// Get the active manager
		manager, err := mcm.GetActiveManager()
		if err != nil {
			fmt.Printf("  Error getting manager: %v\n\n", err)
			continue
		}

		// Get REST config to see the server
		restConfig, err := manager.ToRESTConfig()
		if err != nil {
			fmt.Printf("  Error getting REST config: %v\n\n", err)
			continue
		}

		fmt.Printf("  Server: %s\n", restConfig.Host)

		// Create a derived Kubernetes instance (like MCP does)
		derived, err := manager.Derived(ctx)
		if err != nil {
			fmt.Printf("  Error creating derived: %v\n\n", err)
			continue
		}

		// Get the underlying manager from derived
		derivedManager, err := derived.GetManager()
		if err != nil {
			fmt.Printf("  Error getting derived manager: %v\n\n", err)
			continue
		}

		// Get the discovery client for info (but we'll use dynamic client for nodes)
		_, err = derivedManager.ToDiscoveryClient()
		if err != nil {
			fmt.Printf("  Error getting discovery client: %v\n\n", err)
		}

		// Access the dynamic client directly from the Manager
		// This is a simplification for testing
		dynamicClient := derivedManager.GetDynamicClient()
		if dynamicClient == nil {
			fmt.Printf("  Error: dynamic client is nil\n\n")
			continue
		}

		// Try listing nodes using dynamic client
		nodeGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "nodes",
		}

		nodes, err := dynamicClient.Resource(nodeGVR).List(ctx, metav1.ListOptions{Limit: 2})
		if err != nil {
			fmt.Printf("  Error listing nodes: %v\n\n", err)
			continue
		}

		fmt.Printf("  Nodes found: %d\n", len(nodes.Items))
		for i, node := range nodes.Items {
			if i >= 2 {
				break
			}
			name := node.GetName()

			// Try to get the internal IP
			addresses, found, _ := unstructured.NestedSlice(node.Object, "status", "addresses")
			ip := "unknown"
			if found && len(addresses) > 0 {
				if addr, ok := addresses[0].(map[string]interface{}); ok {
					if address, ok := addr["address"].(string); ok {
						ip = address
					}
				}
			}

			fmt.Printf("    - %s (IP: %s)\n", name, ip)
		}
		fmt.Println()
	}

	// Now test switching back and forth rapidly
	fmt.Println("=== Testing Rapid Switching ===\n")

	for i := 0; i < 3; i++ {
		for _, clusterName := range clusters {
			if err := mcm.SwitchCluster(clusterName); err != nil {
				fmt.Printf("Error switching to %s: %v\n", clusterName, err)
				continue
			}

			manager, _ := mcm.GetActiveManager()
			restConfig, _ := manager.ToRESTConfig()

			// Quick node check
			derived, _ := manager.Derived(ctx)
			derivedManager, _ := derived.GetManager()

			dynamicClient := derivedManager.GetDynamicClient()
			if dynamicClient == nil {
				fmt.Printf("Error: dynamic client is nil for %s\n", clusterName)
				continue
			}

			nodeGVR := schema.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "nodes",
			}

			nodes, _ := dynamicClient.Resource(nodeGVR).List(ctx, metav1.ListOptions{Limit: 1})
			nodeName := "unknown"
			if len(nodes.Items) > 0 {
				nodeName = nodes.Items[0].GetName()
			}

			fmt.Printf("Cluster %s -> Server: %s, First node: %s\n",
				clusterName, restConfig.Host, nodeName)
		}
		fmt.Println()
	}
}
