package kubernetes

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
)

// TestSimplifiedManager_NewSimplifiedManager tests the creation of SimplifiedManager
func TestSimplifiedManager_NewSimplifiedManager(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		clusterName    string
		kubeconfigPath string
		staticConfig   *config.StaticConfig
		setupFunc      func(t *testing.T) string
		wantErr        bool
		errContains    string
	}{
		{
			name:           "empty kubeconfig path",
			clusterName:    "test-cluster",
			kubeconfigPath: "",
			staticConfig:   &config.StaticConfig{},
			wantErr:        true,
			errContains:    "kubeconfig path is required",
		},
		{
			name:        "valid kubeconfig",
			clusterName: "test-cluster",
			setupFunc: func(t *testing.T) string {
				return createTestKubeconfig(t, "test-cluster", "https://test-cluster.example.com:6443")
			},
			staticConfig: &config.StaticConfig{},
			wantErr:      false,
		},
		{
			name:           "invalid kubeconfig path",
			clusterName:    "test-cluster",
			kubeconfigPath: "/non/existent/path/kubeconfig.yaml",
			staticConfig:   &config.StaticConfig{},
			wantErr:        true,
			errContains:    "failed to build config from kubeconfig",
		},
		{
			name:        "rancher proxy endpoint",
			clusterName: "rancher-cluster",
			setupFunc: func(t *testing.T) string {
				return createTestKubeconfig(t, "rancher-cluster",
					"https://rancher.example.com/k8s/clusters/c-m-xxxxx")
			},
			staticConfig: &config.StaticConfig{},
			wantErr:      false,
		},
		{
			name:         "nil static config",
			clusterName:  "test-cluster",
			staticConfig: nil,
			setupFunc: func(t *testing.T) string {
				return createTestKubeconfig(t, "test-cluster", "https://test-cluster.example.com:6443")
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kubeconfigPath := tt.kubeconfigPath
			if tt.setupFunc != nil {
				kubeconfigPath = tt.setupFunc(t)
			}

			logger := klog.Background()
			mgr, err := NewSimplifiedManager(tt.clusterName, kubeconfigPath, tt.staticConfig, logger)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				assert.Nil(t, mgr)
			} else {
				require.NoError(t, err)
				require.NotNil(t, mgr)
				defer mgr.Close()

				// Verify basic properties
				assert.Equal(t, tt.clusterName, mgr.GetClusterName())
				assert.Equal(t, kubeconfigPath, mgr.GetKubeconfigPath())
				assert.False(t, mgr.IsClosed())

				// Verify clients are initialized
				restConfig, err := mgr.ToRESTConfig()
				assert.NoError(t, err)
				assert.NotNil(t, restConfig)

				discoveryClient, err := mgr.ToDiscoveryClient()
				assert.NoError(t, err)
				assert.NotNil(t, discoveryClient)

				dynamicClient, err := mgr.GetDynamicClient()
				assert.NoError(t, err)
				assert.NotNil(t, dynamicClient)

				kubeClient, err := mgr.GetKubeClient()
				assert.NoError(t, err)
				assert.NotNil(t, kubeClient)
			}
		})
	}
}

// TestSimplifiedManager_RancherProxyPath tests that Rancher proxy paths are preserved
func TestSimplifiedManager_RancherProxyPath(t *testing.T) {
	t.Parallel()

	// Test various Rancher proxy URL formats
	testCases := []struct {
		name        string
		clusterName string
		serverURL   string
	}{
		{
			name:        "rancher local cluster",
			clusterName: "local",
			serverURL:   "https://rancher.example.com/k8s/clusters/local",
		},
		{
			name:        "rancher downstream cluster",
			clusterName: "c1-prod",
			serverURL:   "https://rancher.example.com/k8s/clusters/c-m-xxxxx",
		},
		{
			name:        "rancher with port",
			clusterName: "test-cluster",
			serverURL:   "https://rancher.example.com:8443/k8s/clusters/c-m-yyyyy",
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			kubeconfigPath := createTestKubeconfig(t, tc.clusterName, tc.serverURL)
			logger := klog.Background()

			mgr, err := NewSimplifiedManager(tc.clusterName, kubeconfigPath, nil, logger)
			require.NoError(t, err)
			require.NotNil(t, mgr)
			defer mgr.Close()

			// Verify the server URL is preserved correctly
			restConfig, err := mgr.ToRESTConfig()
			require.NoError(t, err)
			assert.Equal(t, tc.serverURL, restConfig.Host)
		})
	}
}

// TestSimplifiedManager_Close tests the Close method
func TestSimplifiedManager_Close(t *testing.T) {
	t.Parallel()

	kubeconfigPath := createTestKubeconfig(t, "test-cluster", "https://test-cluster.example.com:6443")
	logger := klog.Background()

	mgr, err := NewSimplifiedManager("test-cluster", kubeconfigPath, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// Verify manager is not closed initially
	assert.False(t, mgr.IsClosed())

	// Close the manager
	err = mgr.Close()
	require.NoError(t, err)

	// Verify manager is closed
	assert.True(t, mgr.IsClosed())

	// Verify methods return errors after close
	_, err = mgr.ToRESTConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "manager is closed")

	_, err = mgr.ToDiscoveryClient()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "manager is closed")

	_, err = mgr.GetDynamicClient()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "manager is closed")

	_, err = mgr.GetKubeClient()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "manager is closed")

	// Closing again should be idempotent
	err = mgr.Close()
	assert.NoError(t, err)
}

// TestSimplifiedManager_ConcurrentAccess tests thread safety
func TestSimplifiedManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	kubeconfigPath := createTestKubeconfig(t, "test-cluster", "https://test-cluster.example.com:6443")
	logger := klog.Background()

	mgr, err := NewSimplifiedManager("test-cluster", kubeconfigPath, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, mgr)
	defer mgr.Close()

	// Run concurrent operations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()

			// Perform various read operations concurrently
			_ = mgr.GetClusterName()
			_ = mgr.GetKubeconfigPath()
			_, _ = mgr.ToRESTConfig()
			_, _ = mgr.ToDiscoveryClient()
			_, _ = mgr.GetDynamicClient()
			_, _ = mgr.GetKubeClient()
			_ = mgr.IsClosed()
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent operations")
		}
	}
}

// TestSimplifiedManager_ConvertToManager tests conversion to Manager for compatibility
func TestSimplifiedManager_ConvertToManager(t *testing.T) {
	t.Parallel()

	kubeconfigPath := createTestKubeconfig(t, "test-cluster", "https://test-cluster.example.com:6443")
	logger := klog.Background()

	staticConfig := &config.StaticConfig{
		ReadOnly: true,
	}

	mgr, err := NewSimplifiedManager("test-cluster", kubeconfigPath, staticConfig, logger)
	require.NoError(t, err)
	require.NotNil(t, mgr)
	defer mgr.Close()

	// Convert to Manager
	convertedMgr := mgr.ConvertToManager()
	require.NotNil(t, convertedMgr)

	// Verify the converted manager has the same configuration
	restConfig, err := convertedMgr.ToRESTConfig()
	require.NoError(t, err)
	assert.NotNil(t, restConfig)

	originalConfig, err := mgr.ToRESTConfig()
	require.NoError(t, err)
	assert.Equal(t, originalConfig.Host, restConfig.Host)

	// Verify discovery client works
	discoveryClient, err := convertedMgr.ToDiscoveryClient()
	require.NoError(t, err)
	assert.NotNil(t, discoveryClient)
}

// TestSimplifiedManager_ContextCancellation tests context cancellation behavior
func TestSimplifiedManager_ContextCancellation(t *testing.T) {
	t.Parallel()

	kubeconfigPath := createTestKubeconfig(t, "test-cluster", "https://test-cluster.example.com:6443")
	logger := klog.Background()

	mgr, err := NewSimplifiedManager("test-cluster", kubeconfigPath, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	ctx := mgr.GetContext()
	require.NotNil(t, ctx)

	// Verify context is not cancelled initially
	select {
	case <-ctx.Done():
		t.Fatal("Context should not be cancelled initially")
	default:
		// Expected
	}

	// Close the manager
	err = mgr.Close()
	require.NoError(t, err)

	// Verify context is cancelled after close
	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(1 * time.Second):
		t.Fatal("Context should be cancelled after close")
	}
}

// Helper function to create a test kubeconfig file
func createTestKubeconfig(t *testing.T, clusterName, serverURL string) string {
	t.Helper()

	tempDir := t.TempDir()
	kubeconfigPath := filepath.Join(tempDir, "kubeconfig.yaml")

	// Create a minimal kubeconfig
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

	return kubeconfigPath
}

// Benchmark tests for performance validation
func BenchmarkSimplifiedManager_Creation(b *testing.B) {
	kubeconfigPath := createTestKubeconfigForBenchmark(b)
	logger := klog.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr, err := NewSimplifiedManager("bench-cluster", kubeconfigPath, nil, logger)
		if err != nil {
			b.Fatal(err)
		}
		mgr.Close()
	}
}

func BenchmarkSimplifiedManager_ConcurrentGetters(b *testing.B) {
	kubeconfigPath := createTestKubeconfigForBenchmark(b)
	logger := klog.Background()

	mgr, err := NewSimplifiedManager("bench-cluster", kubeconfigPath, nil, logger)
	if err != nil {
		b.Fatal(err)
	}
	defer mgr.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = mgr.GetClusterName()
			_, _ = mgr.ToRESTConfig()
		}
	})
}

func createTestKubeconfigForBenchmark(b *testing.B) string {
	b.Helper()

	tempDir := b.TempDir()
	kubeconfigPath := filepath.Join(tempDir, "kubeconfig.yaml")

	config := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"bench-cluster": {
				Server:                "https://bench-cluster.example.com:6443",
				InsecureSkipTLSVerify: true,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"bench-cluster": {
				Cluster:  "bench-cluster",
				AuthInfo: "user",
			},
		},
		CurrentContext: "bench-cluster",
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Token: "bench-test-token",
			},
		},
	}

	err := clientcmd.WriteToFile(config, kubeconfigPath)
	if err != nil {
		b.Fatal(err)
	}

	return kubeconfigPath
}
