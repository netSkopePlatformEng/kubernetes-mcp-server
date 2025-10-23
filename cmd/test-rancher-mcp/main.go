package main

import (
	"context"
	"fmt"
	"log"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
	"k8s.io/klog/v2"
)

func main() {
	// Create Rancher config
	cfg := &config.RancherConfig{
		Enabled:   true,
		URL:       "https://rancher.prime.iad0.netskope.com",
		Token:     "token-64l2k:dfnjc76lcgthfn8s4wlzqmpjsvfljvxtgvqb2224z2bmkzsrx4qszx",
		Insecure:  true,
		ConfigDir: "/tmp/rancher-test",
	}

	// Create multi-cluster manager (stub for testing)
	staticCfg := &config.StaticConfig{
		KubeConfigDir: "/tmp/rancher-test",
	}
	logger := klog.Background()
	mcm, err := kubernetes.NewMultiClusterManager(staticCfg, logger)
	if err != nil {
		log.Fatalf("Failed to create multi-cluster manager: %v", err)
	}

	// Create Rancher integration
	rancher := kubernetes.NewRancherIntegration(cfg, mcm)

	ctx := context.Background()

	// Test 1: List clusters
	fmt.Println("=== Test 1: List Clusters ===")
	clusters, err := rancher.ListClusters(ctx)
	if err != nil {
		log.Fatalf("Failed to list clusters: %v", err)
	}
	fmt.Printf("Found %d clusters\n", len(clusters))
	for i, name := range clusters[:5] { // Show first 5
		fmt.Printf("%d. %s\n", i+1, name)
	}

	// Test 2: Download single cluster
	if len(clusters) > 0 {
		fmt.Printf("\n=== Test 2: Download Single Cluster ===\n")
		testCluster := clusters[0]
		fmt.Printf("Downloading: %s\n", testCluster)

		if err := rancher.DownloadKubeconfig(ctx, testCluster, "/tmp/rancher-test"); err != nil {
			log.Fatalf("Failed to download kubeconfig: %v", err)
		}

		fmt.Printf("Successfully downloaded kubeconfig for %s\n", testCluster)
	}

	// Test 3: Get status
	fmt.Println("\n=== Test 3: Get Status ===")
	status := rancher.GetStatus()
	fmt.Printf("Enabled: %v\n", status["enabled"])
	fmt.Printf("Rancher URL: %v\n", status["rancher_url"])
	fmt.Printf("Config Dir: %v\n", status["config_dir"])
	fmt.Printf("Last Refresh: %v\n", status["last_refresh"])
	fmt.Printf("Cluster Count: %v\n", status["cluster_count"])

	fmt.Println("\n✅ All tests passed!")
}
