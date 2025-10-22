package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// debugRoundTripper wraps an http.RoundTripper and logs requests
type debugRoundTripper struct {
	delegate http.RoundTripper
}

func (d *debugRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Log the request
	dump, _ := httputil.DumpRequestOut(req, false)
	fmt.Printf("=== HTTP Request ===\n%s\n", dump)
	fmt.Printf("Full URL: %s\n\n", req.URL.String())

	// Forward to the actual round tripper
	return d.delegate.RoundTrip(req)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run debug-rancher-proxy.go <kubeconfig-path>")
		os.Exit(1)
	}

	kubeconfigPath := os.Args[1]
	klog.InitFlags(nil)

	fmt.Printf("Loading kubeconfig from: %s\n\n", kubeconfigPath)

	// Method 1: Using BuildConfigFromFlags (what SimplifiedManager does)
	fmt.Println("=== Method 1: BuildConfigFromFlags ===")
	config1, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Host: %s\n", config1.Host)
	fmt.Printf("APIPath: %s\n", config1.APIPath)

	// Add debug round tripper
	config1.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &debugRoundTripper{delegate: rt}
	})

	client1, err := kubernetes.NewForConfig(config1)
	if err != nil {
		panic(err)
	}

	fmt.Println("\nListing nodes with Method 1:")
	nodes1, err := client1.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{Limit: 1})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else if len(nodes1.Items) > 0 {
		fmt.Printf("First node: %s (IP: %s)\n", nodes1.Items[0].Name, nodes1.Items[0].Status.Addresses[0].Address)
	}

	fmt.Println("\n" + strings.Repeat("=", 80) + "\n")

	// Method 2: Using LoadingRules (what original Manager does)
	fmt.Println("=== Method 2: LoadingRules + ClientConfig ===")
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfigPath
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{})

	config2, err := clientConfig.ClientConfig()
	if err != nil {
		panic(err)
	}
	fmt.Printf("Host: %s\n", config2.Host)
	fmt.Printf("APIPath: %s\n", config2.APIPath)

	// Add debug round tripper
	config2.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &debugRoundTripper{delegate: rt}
	})

	client2, err := kubernetes.NewForConfig(config2)
	if err != nil {
		panic(err)
	}

	fmt.Println("\nListing nodes with Method 2:")
	nodes2, err := client2.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{Limit: 1})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else if len(nodes2.Items) > 0 {
		fmt.Printf("First node: %s (IP: %s)\n", nodes2.Items[0].Name, nodes2.Items[0].Status.Addresses[0].Address)
	}

	fmt.Println("\n" + strings.Repeat("=", 80) + "\n")

	// Method 3: Direct REST client (closer to what kubectl might do)
	fmt.Println("=== Method 3: Direct REST Client ===")

	// First, let's see what the raw config looks like
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		panic(err)
	}

	currentContext := rawConfig.CurrentContext
	if cluster, ok := rawConfig.Clusters[currentContext]; ok {
		fmt.Printf("Raw cluster server from kubeconfig: %s\n", cluster.Server)
	}

	// Create a REST client with explicit path preservation
	restClient := client1.CoreV1().RESTClient()

	// Try to make a direct request preserving the path
	result := restClient.Get().
		AbsPath(config1.Host+"/api/v1/nodes").
		Param("limit", "1").
		Do(context.Background())

	if err := result.Error(); err != nil {
		fmt.Printf("Direct REST request error: %v\n", err)
	} else {
		fmt.Println("Direct REST request succeeded")
	}
}
