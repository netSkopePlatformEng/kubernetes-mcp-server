package main

import (
	"context"
	"fmt"
	"os"

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

	// List all clusters
	allClusters := mcm.ListClusters()
	fmt.Printf("Found %d clusters\n\n", len(allClusters))

	// Test switching between clusters
	clusters := []string{"local", "dp-iad0", "c1-borderlesswan-dev-01-iad0-nc4"}

	for _, clusterName := range clusters {
		fmt.Printf("=== Testing cluster: %s ===\n", clusterName)

		// Switch cluster
		if err := mcm.SwitchCluster(clusterName); err != nil {
			fmt.Printf("Error switching: %v\n\n", err)
			continue
		}

		// Verify active cluster
		active := mcm.GetActiveCluster()
		fmt.Printf("Active cluster: %s\n", active)

		// Get active manager
		manager, err := mcm.GetActiveManager()
		if err != nil {
			fmt.Printf("Error getting manager: %v\n\n", err)
			continue
		}

		// Get REST config to see which server we're pointing to
		restConfig, err := manager.ToRESTConfig()
		if err != nil {
			fmt.Printf("Error getting REST config: %v\n\n", err)
			continue
		}

		fmt.Printf("Server: %s\n\n", restConfig.Host)
	}

	// Stop
	mcm.Stop()
}
