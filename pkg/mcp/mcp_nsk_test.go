package mcp

import (
	"context"
	"os"
	"path"
	"testing"

	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/output"
)

// Test MCP server initialization with multi-cluster and NSK support
func TestNewServer_WithMultiClusterAndNSK(t *testing.T) {
	tempDir := t.TempDir()
	kubeconfigDir := path.Join(tempDir, "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		t.Fatalf("failed to create kubeconfig directory: %v", err)
	}

	// Create a test kubeconfig
	kubeconfigContent := `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test-cluster.example.com
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    username: test-username
    password: test-password
`
	cluster1Path := path.Join(kubeconfigDir, "cluster1.yaml")
	if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
	}

	tests := []struct {
		name         string
		staticConfig *config.StaticConfig
		expectNSK    bool
		expectMulti  bool
	}{
		{
			name: "Multi-cluster with NSK enabled",
			staticConfig: &config.StaticConfig{
				KubeConfigDir: kubeconfigDir,
				NSKIntegration: &config.NSKConfig{
					Enabled:         true,
					RancherURL:      "https://rancher.test.com",
					RancherToken:    "test-token",
					ConfigDir:       tempDir,
					AutoRefresh:     true,
					RefreshInterval: "1h",
				},
			},
			expectNSK:   true,
			expectMulti: true,
		},
		{
			name: "Multi-cluster without NSK",
			staticConfig: &config.StaticConfig{
				KubeConfigDir: kubeconfigDir,
			},
			expectNSK:   false,
			expectMulti: true,
		},
		{
			name: "Single cluster mode",
			staticConfig: &config.StaticConfig{
				KubeConfig: cluster1Path,
			},
			expectNSK:   false,
			expectMulti: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration := Configuration{
				Profile:      ProfileFromString("full"),
				ListOutput:   output.FromString("table"),
				StaticConfig: tt.staticConfig,
			}

			server, err := NewServer(configuration)
			if err != nil {
				t.Fatalf("NewServer failed: %v", err)
			}

			// Check multi-cluster initialization
			if tt.expectMulti {
				if server.clusterManager == nil {
					t.Error("Expected clusterManager to be initialized")
				}
			} else {
				if server.clusterManager != nil {
					t.Error("Expected clusterManager to be nil for single-cluster mode")
				}
			}

			// Check NSK initialization
			if tt.expectNSK {
				if server.nsk == nil {
					t.Error("Expected NSK integration to be initialized")
				}
			} else {
				if server.nsk != nil {
					t.Error("Expected NSK integration to be nil")
				}
			}

			// Clean up
			server.Close()
		})
	}
}

// Test initializeMultiCluster function
func TestServer_initializeMultiCluster(t *testing.T) {
	tempDir := t.TempDir()
	kubeconfigDir := path.Join(tempDir, "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		t.Fatalf("failed to create kubeconfig directory: %v", err)
	}

	tests := []struct {
		name         string
		staticConfig *config.StaticConfig
		expectError  bool
	}{
		{
			name: "With NSK integration",
			staticConfig: &config.StaticConfig{
				KubeConfigDir: kubeconfigDir,
				NSKIntegration: &config.NSKConfig{
					Enabled:    true,
					RancherURL: "https://rancher.test.com",
					ConfigDir:  tempDir,
				},
			},
			expectError: false,
		},
		{
			name: "Without NSK integration",
			staticConfig: &config.StaticConfig{
				KubeConfigDir: kubeconfigDir,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration := Configuration{
				Profile:      ProfileFromString("full"),
				ListOutput:   output.FromString("table"),
				StaticConfig: tt.staticConfig,
			}

			server := &Server{
				configuration: &configuration,
			}

			err := server.initializeMultiCluster()

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if err == nil {
				if server.clusterManager == nil {
					t.Error("Expected clusterManager to be initialized")
				}

				if tt.staticConfig.IsNSKEnabled() && server.nsk == nil {
					t.Error("Expected NSK integration to be initialized when NSK is enabled")
				}

				if !tt.staticConfig.IsNSKEnabled() && server.nsk != nil {
					t.Error("Expected NSK integration to be nil when NSK is disabled")
				}
			}
		})
	}
}

// Test reloadKubernetesClient with multi-cluster
func TestServer_reloadKubernetesClientWithMultiCluster(t *testing.T) {
	tempDir := t.TempDir()
	kubeconfigDir := path.Join(tempDir, "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		t.Fatalf("failed to create kubeconfig directory: %v", err)
	}

	// Create test kubeconfigs
	kubeconfigContent := `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test-cluster.example.com
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    username: test-username
    password: test-password
`
	cluster1Path := path.Join(kubeconfigDir, "cluster1.yaml")
	if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
	}

	tests := []struct {
		name         string
		staticConfig *config.StaticConfig
		multiCluster bool
	}{
		{
			name: "Multi-cluster mode",
			staticConfig: &config.StaticConfig{
				KubeConfigDir: kubeconfigDir,
			},
			multiCluster: true,
		},
		{
			name: "Single-cluster mode",
			staticConfig: &config.StaticConfig{
				KubeConfig: cluster1Path,
			},
			multiCluster: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration := Configuration{
				Profile:      ProfileFromString("full"),
				ListOutput:   output.FromString("table"),
				StaticConfig: tt.staticConfig,
			}

			server := &Server{
				configuration: &configuration,
			}

			if tt.multiCluster {
				// Initialize multi-cluster components
				server.k8s = &kubernetes.Kubernetes{}
				mcm, err := kubernetes.NewMultiClusterManager(tt.staticConfig, klog.Background())
				if err != nil {
					t.Fatalf("Failed to create multi-cluster manager: %v", err)
				}
				server.clusterManager = mcm

				// Discover clusters
				ctx := context.Background()
				if err := mcm.DiscoverClusters(ctx); err != nil {
					t.Logf("DiscoverClusters error (expected for test): %v", err)
				}
			}

			// Test reloadKubernetesClient
			err := server.reloadKubernetesClient()

			// For test environment, we expect errors due to invalid kubeconfigs
			// but the function should handle them gracefully
			if err == nil && server.k == nil {
				t.Error("Expected k (Manager) to be initialized after reload")
			}

			if tt.multiCluster && server.k8s == nil {
				t.Error("Expected k8s (Kubernetes) to be preserved in multi-cluster mode")
			}
		})
	}
}

// Test server methods are in clusters_test.go
// This file focuses on NSK-specific initialization tests
