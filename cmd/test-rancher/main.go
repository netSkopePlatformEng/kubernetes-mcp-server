package main

import (
	"fmt"
	"log"
	"os"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/rancher"
)

func main() {
	// Create client
	client := rancher.NewClient(
		"https://rancher.prime.iad0.netskope.com",
		"token-64l2k:dfnjc76lcgthfn8s4wlzqmpjsvfljvxtgvqb2224z2bmkzsrx4qszx",
		true, // insecure
	)

	// List clusters
	fmt.Println("Listing clusters...")
	clusters, err := client.ListClusters()
	if err != nil {
		log.Fatalf("Failed to list clusters: %v", err)
	}

	fmt.Printf("Found %d clusters\n\n", len(clusters))
	for i, cluster := range clusters[:5] { // Show first 5
		fmt.Printf("%d. %s (ID: %s, State: %s)\n", i+1, cluster.Name, cluster.ID, cluster.State)
	}

	// Test downloading kubeconfig for first cluster
	if len(clusters) > 0 {
		fmt.Printf("\nGenerating kubeconfig for: %s\n", clusters[0].Name)
		kubeconfig, err := client.GenerateKubeconfig(&clusters[0])
		if err != nil {
			log.Fatalf("Failed to generate kubeconfig: %v", err)
		}

		// Save to file
		filename := fmt.Sprintf("/tmp/%s.yaml", clusters[0].Name)
		if err := os.WriteFile(filename, []byte(kubeconfig), 0600); err != nil {
			log.Fatalf("Failed to write kubeconfig: %v", err)
		}

		fmt.Printf("Kubeconfig saved to: %s\n", filename)
		fmt.Printf("Kubeconfig length: %d bytes\n", len(kubeconfig))
	}
}
