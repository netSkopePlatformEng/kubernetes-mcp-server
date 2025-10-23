package kubernetes

import (
	"context"
	"errors"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"k8s.io/klog/v2"
)

func TestManager_Derived(t *testing.T) {
	// Create a temporary kubeconfig file for testing
	tempDir := t.TempDir()
	kubeconfigPath := path.Join(tempDir, "config")
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
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create kubeconfig file: %v", err)
	}

	t.Run("without authorization header returns original manager", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig:    kubeconfigPath,
			DisabledTools: []string{"configuration_view"},
			DeniedResources: []config.GroupVersionKind{
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
		}

		testManager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		defer testManager.Close()
		ctx := context.Background()
		derived, err := testManager.Derived(ctx)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		if derived.manager != testManager {
			t.Errorf("expected original manager, got different manager")
		}
	})

	t.Run("with invalid authorization header returns original manager", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig:    kubeconfigPath,
			DisabledTools: []string{"configuration_view"},
			DeniedResources: []config.GroupVersionKind{
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
		}

		testManager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		defer testManager.Close()
		ctx := context.WithValue(context.Background(), OAuthAuthorizationHeader, "invalid-token")
		derived, err := testManager.Derived(ctx)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		if derived.manager != testManager {
			t.Errorf("expected original manager, got different manager")
		}
	})

	t.Run("with valid bearer token creates derived manager with correct configuration", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig:    kubeconfigPath,
			DisabledTools: []string{"configuration_view"},
			DeniedResources: []config.GroupVersionKind{
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
		}

		testManager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		defer testManager.Close()
		testBearerToken := "test-bearer-token-123"
		ctx := context.WithValue(context.Background(), OAuthAuthorizationHeader, "Bearer "+testBearerToken)
		derived, err := testManager.Derived(ctx)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		if derived.manager == testManager {
			t.Errorf("expected new derived manager, got original manager")
		}

		if derived.manager.staticConfig != testStaticConfig {
			t.Errorf("staticConfig not properly wired to derived manager")
		}

		derivedCfg := derived.manager.cfg
		if derivedCfg == nil {
			t.Fatalf("derived config is nil")
		}

		originalCfg := testManager.cfg
		if derivedCfg.Host != originalCfg.Host {
			t.Errorf("expected Host %s, got %s", originalCfg.Host, derivedCfg.Host)
		}
		if derivedCfg.APIPath != originalCfg.APIPath {
			t.Errorf("expected APIPath %s, got %s", originalCfg.APIPath, derivedCfg.APIPath)
		}
		if derivedCfg.QPS != originalCfg.QPS {
			t.Errorf("expected QPS %f, got %f", originalCfg.QPS, derivedCfg.QPS)
		}
		if derivedCfg.Burst != originalCfg.Burst {
			t.Errorf("expected Burst %d, got %d", originalCfg.Burst, derivedCfg.Burst)
		}
		if derivedCfg.Timeout != originalCfg.Timeout {
			t.Errorf("expected Timeout %v, got %v", originalCfg.Timeout, derivedCfg.Timeout)
		}

		if derivedCfg.Insecure != originalCfg.Insecure {
			t.Errorf("expected TLS Insecure %v, got %v", originalCfg.Insecure, derivedCfg.Insecure)
		}
		if derivedCfg.ServerName != originalCfg.ServerName {
			t.Errorf("expected TLS ServerName %s, got %s", originalCfg.ServerName, derivedCfg.ServerName)
		}
		if derivedCfg.CAFile != originalCfg.CAFile {
			t.Errorf("expected TLS CAFile %s, got %s", originalCfg.CAFile, derivedCfg.CAFile)
		}
		if string(derivedCfg.CAData) != string(originalCfg.CAData) {
			t.Errorf("expected TLS CAData %s, got %s", string(originalCfg.CAData), string(derivedCfg.CAData))
		}

		if derivedCfg.BearerToken != testBearerToken {
			t.Errorf("expected BearerToken %s, got %s", testBearerToken, derivedCfg.BearerToken)
		}
		if derivedCfg.UserAgent != CustomUserAgent {
			t.Errorf("expected UserAgent %s, got %s", CustomUserAgent, derivedCfg.UserAgent)
		}

		// Verify that sensitive fields are NOT copied to prevent credential leakage
		// The derived config should only use the bearer token from the Authorization header
		// and not inherit any authentication credentials from the original kubeconfig
		if derivedCfg.CertFile != "" {
			t.Errorf("expected TLS CertFile to be empty, got %s", derivedCfg.CertFile)
		}
		if derivedCfg.KeyFile != "" {
			t.Errorf("expected TLS KeyFile to be empty, got %s", derivedCfg.KeyFile)
		}
		if len(derivedCfg.CertData) != 0 {
			t.Errorf("expected TLS CertData to be empty, got %v", derivedCfg.CertData)
		}
		if len(derivedCfg.KeyData) != 0 {
			t.Errorf("expected TLS KeyData to be empty, got %v", derivedCfg.KeyData)
		}

		if derivedCfg.Username != "" {
			t.Errorf("expected Username to be empty, got %s", derivedCfg.Username)
		}
		if derivedCfg.Password != "" {
			t.Errorf("expected Password to be empty, got %s", derivedCfg.Password)
		}
		if derivedCfg.AuthProvider != nil {
			t.Errorf("expected AuthProvider to be nil, got %v", derivedCfg.AuthProvider)
		}
		if derivedCfg.ExecProvider != nil {
			t.Errorf("expected ExecProvider to be nil, got %v", derivedCfg.ExecProvider)
		}
		if derivedCfg.BearerTokenFile != "" {
			t.Errorf("expected BearerTokenFile to be empty, got %s", derivedCfg.BearerTokenFile)
		}
		if derivedCfg.Impersonate.UserName != "" {
			t.Errorf("expected Impersonate.UserName to be empty, got %s", derivedCfg.Impersonate.UserName)
		}

		// Verify that the original manager still has the sensitive data
		if originalCfg.Username == "" && originalCfg.Password == "" {
			t.Logf("original kubeconfig shouldn't be modified")
		}

		// Verify that the derived manager has proper clients initialized
		if derived.manager.accessControlClientSet == nil {
			t.Error("expected accessControlClientSet to be initialized")
		}
		if derived.manager.accessControlClientSet.staticConfig != testStaticConfig {
			t.Errorf("staticConfig not properly wired to derived manager")
		}
		if derived.manager.discoveryClient == nil {
			t.Error("expected discoveryClient to be initialized")
		}
		if derived.manager.accessControlRESTMapper == nil {
			t.Error("expected accessControlRESTMapper to be initialized")
		}
		if derived.manager.accessControlRESTMapper.staticConfig != testStaticConfig {
			t.Errorf("staticConfig not properly wired to derived manager")
		}
		if derived.manager.dynamicClient == nil {
			t.Error("expected dynamicClient to be initialized")
		}
	})

	t.Run("with RequireOAuth=true and no authorization header returns oauth token required error", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig:    kubeconfigPath,
			RequireOAuth:  true,
			DisabledTools: []string{"configuration_view"},
			DeniedResources: []config.GroupVersionKind{
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
		}

		testManager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		defer testManager.Close()
		ctx := context.Background()
		derived, err := testManager.Derived(ctx)
		if err == nil {
			t.Fatal("expected error for missing oauth token, got nil")
		}
		if err.Error() != "oauth token required" {
			t.Fatalf("expected error 'oauth token required', got %s", err.Error())
		}
		if derived != nil {
			t.Error("expected nil derived manager when oauth token required")
		}
	})

	t.Run("with RequireOAuth=true and invalid authorization header returns oauth token required error", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig:    kubeconfigPath,
			RequireOAuth:  true,
			DisabledTools: []string{"configuration_view"},
			DeniedResources: []config.GroupVersionKind{
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
		}

		testManager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		defer testManager.Close()
		ctx := context.WithValue(context.Background(), OAuthAuthorizationHeader, "invalid-token")
		derived, err := testManager.Derived(ctx)
		if err == nil {
			t.Fatal("expected error for invalid oauth token, got nil")
		}
		if err.Error() != "oauth token required" {
			t.Fatalf("expected error 'oauth token required', got %s", err.Error())
		}
		if derived != nil {
			t.Error("expected nil derived manager when oauth token required")
		}
	})

	t.Run("with RequireOAuth=true and valid bearer token creates derived manager", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig:    kubeconfigPath,
			RequireOAuth:  true,
			DisabledTools: []string{"configuration_view"},
			DeniedResources: []config.GroupVersionKind{
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
		}

		testManager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		defer testManager.Close()
		testBearerToken := "test-bearer-token-123"
		ctx := context.WithValue(context.Background(), OAuthAuthorizationHeader, "Bearer "+testBearerToken)
		derived, err := testManager.Derived(ctx)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		if derived.manager == testManager {
			t.Error("expected new derived manager, got original manager")
		}

		if derived.manager.staticConfig != testStaticConfig {
			t.Error("staticConfig not properly wired to derived manager")
		}

		derivedCfg := derived.manager.cfg
		if derivedCfg == nil {
			t.Fatal("derived config is nil")
		}

		if derivedCfg.BearerToken != testBearerToken {
			t.Errorf("expected BearerToken %s, got %s", testBearerToken, derivedCfg.BearerToken)
		}
	})
}

// Test suite for resource cleanup functionality
func TestManager_ResourceCleanup(t *testing.T) {
	// Create a temporary kubeconfig file for testing
	tempDir := t.TempDir()
	kubeconfigPath := path.Join(tempDir, "config")
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
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create kubeconfig file: %v", err)
	}

	t.Run("Close calls all cleanup functions", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		manager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		// Track cleanup function calls
		var cleanupCalls []int
		var mu sync.Mutex

		// Register multiple cleanup functions
		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCalls = append(cleanupCalls, 1)
			mu.Unlock()
			return nil
		})

		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCalls = append(cleanupCalls, 2)
			mu.Unlock()
			return nil
		})

		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCalls = append(cleanupCalls, 3)
			mu.Unlock()
			return nil
		})

		// Close the manager
		manager.Close()

		// Verify all cleanup functions were called
		mu.Lock()
		defer mu.Unlock()
		if len(cleanupCalls) != 3 {
			t.Errorf("expected 3 cleanup calls, got %d", len(cleanupCalls))
		}

		expectedOrder := []int{1, 2, 3}
		for i, expected := range expectedOrder {
			if i >= len(cleanupCalls) || cleanupCalls[i] != expected {
				t.Errorf("expected cleanup call %d at position %d, got %v", expected, i, cleanupCalls)
			}
		}
	})

	t.Run("Close handles cleanup function errors gracefully", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		manager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		var cleanupCalls []string
		var mu sync.Mutex

		// Register cleanup functions that succeed and fail
		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCalls = append(cleanupCalls, "success1")
			mu.Unlock()
			return nil
		})

		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCalls = append(cleanupCalls, "error1")
			mu.Unlock()
			return errors.New("cleanup failed")
		})

		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCalls = append(cleanupCalls, "success2")
			mu.Unlock()
			return nil
		})

		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCalls = append(cleanupCalls, "error2")
			mu.Unlock()
			return errors.New("another cleanup failed")
		})

		// Close should continue despite errors
		manager.Close()

		// Verify all cleanup functions were called despite errors
		mu.Lock()
		defer mu.Unlock()
		expectedCalls := []string{"success1", "error1", "success2", "error2"}
		if len(cleanupCalls) != len(expectedCalls) {
			t.Errorf("expected %d cleanup calls, got %d: %v", len(expectedCalls), len(cleanupCalls), cleanupCalls)
		}

		for i, expected := range expectedCalls {
			if i >= len(cleanupCalls) || cleanupCalls[i] != expected {
				t.Errorf("expected cleanup call %q at position %d, got %v", expected, i, cleanupCalls)
			}
		}
	})

	t.Run("Context cancellation stops long-running operations", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		manager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		defer manager.Close()

		// Get the manager's context
		ctx := manager.GetContext()

		// Start a goroutine that should be cancelled
		done := make(chan bool)
		cancelled := false

		go func() {
			select {
			case <-ctx.Done():
				cancelled = true
				done <- true
			case <-time.After(5 * time.Second):
				done <- false
			}
		}()

		// Cancel the manager's context
		manager.Close()

		// Wait for the goroutine to complete
		select {
		case result := <-done:
			if !result {
				t.Error("Context was not cancelled within timeout")
			}
			if !cancelled {
				t.Error("Goroutine was not cancelled by context")
			}
		case <-time.After(1 * time.Second):
			t.Error("Test timed out waiting for context cancellation")
		}
	})

	t.Run("Multiple Close calls are safe", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		manager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		// Track cleanup function calls
		var cleanupCount int
		var mu sync.Mutex

		manager.RegisterCleanup(func() error {
			mu.Lock()
			cleanupCount++
			mu.Unlock()
			return nil
		})

		// Call Close multiple times
		manager.Close()
		manager.Close()
		manager.Close()

		// Cleanup should only be called once (first time)
		mu.Lock()
		defer mu.Unlock()
		if cleanupCount != 1 {
			t.Errorf("expected cleanup to be called once, got %d calls", cleanupCount)
		}
	})

	t.Run("RegisterCleanup after Close does not add functions", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		manager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		// Close the manager first
		manager.Close()

		// Try to register cleanup after closing
		manager.RegisterCleanup(func() error {
			t.Error("This cleanup function should not be called")
			return nil
		})

		// Call Close again to verify the new function isn't called
		manager.Close()
	})

	t.Run("Discovery client cache is invalidated on Close", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		manager, err := NewManager(testStaticConfig)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}

		// Verify discovery client exists
		if manager.discoveryClient == nil {
			t.Fatal("Discovery client should be initialized")
		}

		// Close the manager
		manager.Close()

		// The discovery client should still exist but its cache should be invalidated
		// We can't easily test the cache invalidation directly, but we can verify
		// that Close completes without errors
	})
}

func TestKubernetes_ResourceCleanup(t *testing.T) {
	// Create a temporary kubeconfig file for testing
	tempDir := t.TempDir()
	kubeconfigPath := path.Join(tempDir, "config")
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
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create kubeconfig file: %v", err)
	}

	t.Run("Single cluster mode cleanup", func(t *testing.T) {
		testStaticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		logger := klog.Background()
		k8s, err := NewKubernetes(testStaticConfig, logger)
		if err != nil {
			t.Fatalf("failed to create Kubernetes instance: %v", err)
		}

		if k8s.IsMultiCluster() {
			t.Error("Expected single cluster mode")
		}

		// Track cleanup
		cleanupCalled := false
		if k8s.manager != nil {
			k8s.manager.RegisterCleanup(func() error {
				cleanupCalled = true
				return nil
			})
		}

		// Close should cleanup single manager
		k8s.Close()

		if !cleanupCalled {
			t.Error("Expected cleanup to be called for single cluster mode")
		}
	})

	t.Run("Multi-cluster mode cleanup", func(t *testing.T) {
		tempDir := t.TempDir()

		// Create multiple kubeconfig files
		kubeconfigDir := path.Join(tempDir, "kubeconfigs")
		if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
			t.Fatalf("failed to create kubeconfig directory: %v", err)
		}

		cluster1Path := path.Join(kubeconfigDir, "cluster1.yaml")
		cluster2Path := path.Join(kubeconfigDir, "cluster2.yaml")

		if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
			t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
		}
		if err := os.WriteFile(cluster2Path, []byte(kubeconfigContent), 0644); err != nil {
			t.Fatalf("failed to create cluster2 kubeconfig: %v", err)
		}

		testStaticConfig := &config.StaticConfig{
			KubeConfigDir: kubeconfigDir,
		}

		logger := klog.Background()
		k8s, err := NewKubernetes(testStaticConfig, logger)
		if err != nil {
			t.Fatalf("failed to create Kubernetes instance: %v", err)
		}

		if !k8s.IsMultiCluster() {
			t.Error("Expected multi-cluster mode")
		}

		// Close should cleanup multi-cluster manager
		k8s.Close()

		// Verify that subsequent operations don't crash
		clusters := k8s.ListClusters()
		if clusters != nil {
			t.Error("Expected nil clusters after Close")
		}
	})
}

func TestMultiClusterManager_ResourceCleanup(t *testing.T) {
	tempDir := t.TempDir()
	kubeconfigDir := path.Join(tempDir, "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		t.Fatalf("failed to create kubeconfig directory: %v", err)
	}

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
	cluster2Path := path.Join(kubeconfigDir, "cluster2.yaml")

	if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
	}
	if err := os.WriteFile(cluster2Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster2 kubeconfig: %v", err)
	}

	testStaticConfig := &config.StaticConfig{
		KubeConfigDir: kubeconfigDir,
	}

	logger := klog.Background()

	t.Run("MultiClusterManager Stop cleans up all cluster managers", func(t *testing.T) {
		mcm, err := NewMultiClusterManager(testStaticConfig, logger)
		if err != nil {
			t.Fatalf("failed to create multi-cluster manager: %v", err)
		}

		ctx := context.Background()
		if err := mcm.Start(ctx); err != nil {
			t.Fatalf("failed to start multi-cluster manager: %v", err)
		}

		// Verify clusters were discovered
		clusterCount := mcm.GetClusterCount()
		if clusterCount == 0 {
			t.Error("Expected clusters to be discovered")
		}

		// Stop should clean up all resources
		mcm.Stop()

		// Verify cleanup
		if mcm.GetClusterCount() != 0 {
			t.Error("Expected cluster count to be 0 after Stop")
		}
	})

	t.Run("MultiClusterManager handles health monitor cleanup", func(t *testing.T) {
		mcm, err := NewMultiClusterManager(testStaticConfig, logger)
		if err != nil {
			t.Fatalf("failed to create multi-cluster manager: %v", err)
		}

		ctx := context.Background()
		if err := mcm.Start(ctx); err != nil {
			t.Fatalf("failed to start multi-cluster manager: %v", err)
		}

		// Verify health monitor exists
		if mcm.healthMonitor == nil {
			t.Error("Expected health monitor to be initialized")
		}

		// Stop should clean up health monitor
		mcm.Stop()

		// Health monitor should be stopped (we can't easily test internal state,
		// but Stop should complete without hanging)
	})

	t.Run("MultiClusterManager concurrent cleanup is safe", func(t *testing.T) {
		mcm, err := NewMultiClusterManager(testStaticConfig, logger)
		if err != nil {
			t.Fatalf("failed to create multi-cluster manager: %v", err)
		}

		ctx := context.Background()
		if err := mcm.Start(ctx); err != nil {
			t.Fatalf("failed to start multi-cluster manager: %v", err)
		}

		// Call Stop concurrently
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mcm.Stop()
			}()
		}

		// Wait for all Stop calls to complete
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Success - no deadlocks
		case <-time.After(5 * time.Second):
			t.Fatal("Concurrent Stop calls caused deadlock")
		}
	})
}
