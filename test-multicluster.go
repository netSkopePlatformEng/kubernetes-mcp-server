package main

import (
	"context"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// Test switching between clusters
	clusters := []string{"local", "dp-iad0"}

	for _, clusterName := range clusters {
		fmt.Printf("\n=== Switching to cluster: %s ===\n", clusterName)

		// Switch cluster
		if err := mcm.SwitchCluster(clusterName); err != nil {
			fmt.Printf("Error switching: %v\n", err)
			continue
		}

		// Get active manager
		manager, err := mcm.GetActiveManager()
		if err != nil {
			fmt.Printf("Error getting manager: %v\n", err)
			continue
		}

		// Get REST config to see which server we're pointing to
		restConfig, err := manager.ToRESTConfig()
		if err != nil {
			fmt.Printf("Error getting REST config: %v\n", err)
			continue
		}
		fmt.Printf("Server: %s\n", restConfig.Host)

		// Try to list nodes
		k8s := &kubernetes.Kubernetes{}
		k8s.SetManager(manager)

		derived, err := manager.Derived(ctx)
		if err != nil {
			fmt.Printf("Error getting derived: %v\n", err)
			continue
		}

		clientset, err := derived.GetManager().ToDiscoveryClient()
		if err != nil {
			fmt.Printf("Error getting discovery client: %v\n", err)
			continue
		}

		// Just check server version as a simple test
		version, err := clientset.ServerVersion()
		if err != nil {
			fmt.Printf("Error listing nodes: %v\n", err)
			continue
		}

		fmt.Printf("Nodes:\n")
		for _, node := range nodes.Items {
			fmt.Printf("  - %s\n", node.Name)
		}
	}

	// Stop
	mcm.Stop()
}
