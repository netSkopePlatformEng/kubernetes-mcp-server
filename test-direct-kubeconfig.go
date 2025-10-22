package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func testCluster(name string) {
	fmt.Printf("\n=== Testing %s ===\n", name)

	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".mcp", name+".yaml")

	// Create a fresh config
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		fmt.Printf("Error building config: %v\n", err)
		return
	}

	fmt.Printf("Server: %s\n", config.Host)

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating clientset: %v\n", err)
		return
	}

	// List nodes
	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{Limit: 3})
	if err != nil {
		fmt.Printf("Error listing nodes: %v\n", err)
		return
	}

	fmt.Printf("Nodes:\n")
	for _, node := range nodes.Items {
		var internalIP string
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" {
				internalIP = addr.Address
				break
			}
		}
		fmt.Printf("  - %-40s %s\n", node.Name, internalIP)
	}
}

func main() {
	// Test different clusters
	testCluster("local")
	testCluster("dp-iad0")
	testCluster("c1-borderlesswan-dev-01-iad0-nc4")
}
