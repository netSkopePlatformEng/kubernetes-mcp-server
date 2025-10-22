package main

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	// Test loading different kubeconfig files
	configs := []string{"local", "dp-iad0", "c1-prf-local"}

	for _, name := range configs {
		path := filepath.Join(os.Getenv("HOME"), ".mcp", name+".yaml")

		// Create fresh loading rules each time
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		loadingRules.ExplicitPath = path
		loadingRules.Precedence = []string{path}

		config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules,
			&clientcmd.ConfigOverrides{},
		)

		restConfig, err := config.ClientConfig()
		if err != nil {
			fmt.Printf("Error loading %s: %v\n", name, err)
			continue
		}

		rawConfig, _ := config.RawConfig()
		currentContext := rawConfig.CurrentContext

		fmt.Printf("\n=== %s ===\n", name)
		fmt.Printf("Path: %s\n", path)
		fmt.Printf("Server: %s\n", restConfig.Host)
		fmt.Printf("Current Context: %s\n", currentContext)
		fmt.Printf("Auth: Bearer=%v, Cert=%v\n",
			restConfig.BearerToken != "",
			restConfig.CertFile != "" || len(restConfig.CertData) > 0)
	}
}
