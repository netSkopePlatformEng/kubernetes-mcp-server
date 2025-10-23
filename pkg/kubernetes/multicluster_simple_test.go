package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
)

// TestSimpleMultiClusterManager_NewSimpleMultiClusterManager tests creation of the manager
func TestSimpleMultiClusterManager_NewSimpleMultiClusterManager(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		staticConfig *config.StaticConfig
		wantErr      bool
		errContains  string
	}{
		{
			name: "multi-cluster enabled",
			staticConfig: &config.StaticConfig{
				KubeConfigDir: "/tmp/test",
			},
			wantErr: false,
		},
		{
			name:         "multi-cluster not enabled",
			staticConfig: &config.StaticConfig{},
			wantErr:      true,
			errContains:  "multi-cluster mode not enabled",
		},
		{
			name: "with default cluster",
			staticConfig: &config.StaticConfig{
				KubeConfigDir:  "/tmp/test",
				DefaultCluster: "test-cluster",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger := klog.Background()
			mcm, err := NewSimpleMultiClusterManager(tt.staticConfig, logger)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				assert.Nil(t, mcm)
			} else {
				require.NoError(t, err)
				require.NotNil(t, mcm)
				assert.NotNil(t, mcm.availableClusters)
				assert.Equal(t, tt.staticConfig, mcm.staticConfig)
			}
		})
	}
}

// TestSimpleMultiClusterManager_Start tests starting the manager and discovering clusters
func TestSimpleMultiClusterManager_Start(t *testing.T) {
	t.Parallel()

	// Create a temporary directory with test kubeconfigs
	tempDir := t.TempDir()
	createTestKubeconfigFile(t, tempDir, "cluster1.yaml", "cluster1", "https://cluster1.example.com:6443")
	createTestKubeconfigFile(t, tempDir, "cluster2.yaml", "cluster2", "https://cluster2.example.com:6443")
	createTestKubeconfigFile(t, tempDir, "not-a-kubeconfig.txt", "", "") // Should be ignored

	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = mcm.Start(ctx)
	require.NoError(t, err)
	defer mcm.Stop()

	// Verify clusters were discovered
	assert.Equal(t, 2, mcm.GetClusterCount())

	clusters := mcm.ListClusters()
	assert.Len(t, clusters, 2)

	// Check cluster names
	clusterNames := make(map[string]bool)
	for _, c := range clusters {
		clusterNames[c.Name] = true
	}
	assert.True(t, clusterNames["cluster1"])
	assert.True(t, clusterNames["cluster2"])

	// One cluster should be active
	activeCluster := mcm.GetActiveCluster()
	assert.NotEmpty(t, activeCluster)
	assert.Contains(t, []string{"cluster1", "cluster2"}, activeCluster)
}

// TestSimpleMultiClusterManager_SwitchCluster tests cluster switching
func TestSimpleMultiClusterManager_SwitchCluster(t *testing.T) {
	t.Parallel()

	// Setup test environment
	tempDir := t.TempDir()
	createTestKubeconfigFile(t, tempDir, "cluster1.yaml", "cluster1", "https://cluster1.example.com:6443")
	createTestKubeconfigFile(t, tempDir, "cluster2.yaml", "cluster2", "https://cluster2.example.com:6443")
	createTestKubeconfigFile(t, tempDir, "rancher-cluster.yaml", "rancher-cluster",
		"https://rancher.example.com/k8s/clusters/c-m-xxxxx")

	staticConfig := &config.StaticConfig{
		KubeConfigDir:  tempDir,
		DefaultCluster: "cluster1",
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = mcm.Start(ctx)
	require.NoError(t, err)
	defer mcm.Stop()

	// Verify initial state
	assert.Equal(t, "cluster1", mcm.GetActiveCluster())

	// Test switching to cluster2
	err = mcm.SwitchCluster("cluster2")
	require.NoError(t, err)
	assert.Equal(t, "cluster2", mcm.GetActiveCluster())

	// Get the manager and verify it's for the right cluster
	manager, err := mcm.GetActiveManager()
	require.NoError(t, err)
	require.NotNil(t, manager)

	restConfig, err := manager.ToRESTConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://cluster2.example.com:6443", restConfig.Host)

	// Test switching to Rancher cluster (verify proxy path preservation)
	err = mcm.SwitchCluster("rancher-cluster")
	require.NoError(t, err)
	assert.Equal(t, "rancher-cluster", mcm.GetActiveCluster())

	manager, err = mcm.GetActiveManager()
	require.NoError(t, err)
	require.NotNil(t, manager)

	restConfig, err = manager.ToRESTConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://rancher.example.com/k8s/clusters/c-m-xxxxx", restConfig.Host)

	// Test switching to non-existent cluster
	err = mcm.SwitchCluster("non-existent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster non-existent not found")
	// Should still be on rancher-cluster
	assert.Equal(t, "rancher-cluster", mcm.GetActiveCluster())

	// Test switching back to cluster1
	err = mcm.SwitchCluster("cluster1")
	require.NoError(t, err)
	assert.Equal(t, "cluster1", mcm.GetActiveCluster())
}

// TestSimpleMultiClusterManager_GetManager tests getting managers for specific clusters
func TestSimpleMultiClusterManager_GetManager(t *testing.T) {
	t.Parallel()

	// Setup test environment
	tempDir := t.TempDir()
	createTestKubeconfigFile(t, tempDir, "cluster1.yaml", "cluster1", "https://cluster1.example.com:6443")
	createTestKubeconfigFile(t, tempDir, "cluster2.yaml", "cluster2", "https://cluster2.example.com:6443")

	staticConfig := &config.StaticConfig{
		KubeConfigDir:  tempDir,
		DefaultCluster: "cluster1",
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = mcm.Start(ctx)
	require.NoError(t, err)
	defer mcm.Stop()

	// Get manager for active cluster
	manager1, err := mcm.GetManager("cluster1")
	require.NoError(t, err)
	require.NotNil(t, manager1)

	restConfig1, err := manager1.ToRESTConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://cluster1.example.com:6443", restConfig1.Host)

	// Get manager for non-active cluster (creates temporary manager)
	manager2, err := mcm.GetManager("cluster2")
	require.NoError(t, err)
	require.NotNil(t, manager2)

	restConfig2, err := manager2.ToRESTConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://cluster2.example.com:6443", restConfig2.Host)

	// Note: In production, caller should close temporary managers
	// For testing, we'll let them be garbage collected

	// Get manager for non-existent cluster
	_, err = mcm.GetManager("non-existent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster non-existent not found")
}

// TestSimpleMultiClusterManager_RefreshClusters tests refreshing cluster list
func TestSimpleMultiClusterManager_RefreshClusters(t *testing.T) {
	t.Parallel()

	// Setup test environment
	tempDir := t.TempDir()
	createTestKubeconfigFile(t, tempDir, "cluster1.yaml", "cluster1", "https://cluster1.example.com:6443")

	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = mcm.Start(ctx)
	require.NoError(t, err)
	defer mcm.Stop()

	// Initially should have 1 cluster
	assert.Equal(t, 1, mcm.GetClusterCount())

	// Add another kubeconfig
	createTestKubeconfigFile(t, tempDir, "cluster2.yaml", "cluster2", "https://cluster2.example.com:6443")

	// Refresh clusters
	err = mcm.RefreshClusters(ctx)
	require.NoError(t, err)

	// Should now have 2 clusters
	assert.Equal(t, 2, mcm.GetClusterCount())

	clusters := mcm.ListClusters()
	assert.Len(t, clusters, 2)
}

// TestSimpleMultiClusterManager_ConcurrentAccess tests thread safety
func TestSimpleMultiClusterManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	// Setup test environment
	tempDir := t.TempDir()
	createTestKubeconfigFile(t, tempDir, "cluster1.yaml", "cluster1", "https://cluster1.example.com:6443")
	createTestKubeconfigFile(t, tempDir, "cluster2.yaml", "cluster2", "https://cluster2.example.com:6443")
	createTestKubeconfigFile(t, tempDir, "cluster3.yaml", "cluster3", "https://cluster3.example.com:6443")

	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = mcm.Start(ctx)
	require.NoError(t, err)
	defer mcm.Stop()

	// Run concurrent operations
	var wg sync.WaitGroup
	clusters := []string{"cluster1", "cluster2", "cluster3"}

	// Concurrent switches
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			clusterName := clusters[idx%len(clusters)]
			_ = mcm.SwitchCluster(clusterName)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mcm.GetActiveCluster()
			_ = mcm.ListClusters()
			_ = mcm.GetClusterCount()
		}()
	}

	// Concurrent get managers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			clusterName := clusters[idx%len(clusters)]
			_, _ = mcm.GetManager(clusterName)
		}(i)
	}

	// Wait for all operations to complete
	done := make(chan bool)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// Success
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for concurrent operations")
	}

	// Verify manager is still in valid state
	activeCluster := mcm.GetActiveCluster()
	assert.Contains(t, clusters, activeCluster)
	assert.Equal(t, 3, mcm.GetClusterCount())
}

// TestSimpleMultiClusterManager_Stop tests stopping the manager
func TestSimpleMultiClusterManager_Stop(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	createTestKubeconfigFile(t, tempDir, "cluster1.yaml", "cluster1", "https://cluster1.example.com:6443")

	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = mcm.Start(ctx)
	require.NoError(t, err)

	// Verify manager is active
	assert.NotEmpty(t, mcm.GetActiveCluster())
	manager, err := mcm.GetActiveManager()
	require.NoError(t, err)
	assert.NotNil(t, manager)

	// Stop the manager
	mcm.Stop()

	// Verify cleanup
	assert.Empty(t, mcm.GetActiveCluster())
	_, err = mcm.GetActiveManager()
	assert.Error(t, err)
}

// TestSimpleMultiClusterManager_EmptyDirectory tests behavior with empty kubeconfig directory
func TestSimpleMultiClusterManager_EmptyDirectory(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = mcm.Start(ctx)
	// Should not error even with no clusters
	require.NoError(t, err)
	defer mcm.Stop()

	// Should have no clusters
	assert.Equal(t, 0, mcm.GetClusterCount())
	assert.Empty(t, mcm.GetActiveCluster())

	// Operations should fail gracefully
	err = mcm.SwitchCluster("non-existent")
	assert.Error(t, err)

	_, err = mcm.GetActiveManager()
	assert.Error(t, err)
}

// Helper function to create a test kubeconfig file
func createTestKubeconfigFile(t *testing.T, dir, filename, clusterName, serverURL string) {
	t.Helper()

	if filename == "not-a-kubeconfig.txt" {
		// Create a non-kubeconfig file to test filtering
		filepath := filepath.Join(dir, filename)
		err := os.WriteFile(filepath, []byte("not a kubeconfig"), 0600)
		require.NoError(t, err)
		return
	}

	kubeconfigPath := filepath.Join(dir, filename)

	config := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterName: {
				Server:                serverURL,
				InsecureSkipTLSVerify: true,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			clusterName: {
				Cluster:  clusterName,
				AuthInfo: "user",
			},
		},
		CurrentContext: clusterName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Token: "test-token-" + clusterName,
			},
		},
	}

	err := clientcmd.WriteToFile(config, kubeconfigPath)
	require.NoError(t, err)
}

// Benchmark tests
func BenchmarkSimpleMultiClusterManager_SwitchCluster(b *testing.B) {
	tempDir := b.TempDir()
	for i := 0; i < 10; i++ {
		clusterName := fmt.Sprintf("cluster%d", i)
		createBenchKubeconfig(b, tempDir, fmt.Sprintf("%s.yaml", clusterName),
			clusterName, fmt.Sprintf("https://%s.example.com:6443", clusterName))
	}

	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	logger := klog.Background()
	mcm, err := NewSimpleMultiClusterManager(staticConfig, logger)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	err = mcm.Start(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer mcm.Stop()

	clusters := []string{"cluster0", "cluster1", "cluster2", "cluster3", "cluster4"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clusterName := clusters[i%len(clusters)]
		_ = mcm.SwitchCluster(clusterName)
	}
}

func createBenchKubeconfig(b *testing.B, dir, filename, clusterName, serverURL string) {
	b.Helper()

	kubeconfigPath := filepath.Join(dir, filename)

	config := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterName: {
				Server:                serverURL,
				InsecureSkipTLSVerify: true,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			clusterName: {
				Cluster:  clusterName,
				AuthInfo: "user",
			},
		},
		CurrentContext: clusterName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Token: "bench-token-" + clusterName,
			},
		},
	}

	err := clientcmd.WriteToFile(config, kubeconfigPath)
	if err != nil {
		b.Fatal(err)
	}
}
