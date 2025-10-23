package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
)

func TestScanKubeConfigDirectory(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create test kubeconfig files
	testFiles := map[string]string{
		"cluster1.yaml":                   "apiVersion: v1\nkind: Config",
		"cluster2.yml":                    "apiVersion: v1\nkind: Config",
		"stork-qa01-dp-npe-iad0-nc1.yaml": "apiVersion: v1\nkind: Config",
		"stork-qa01-mp-npe-iad0-nc1.yaml": "apiVersion: v1\nkind: Config",
		"stork-stg01-dp-iad0-nc4.yaml":    "apiVersion: v1\nkind: Config",
		"stork-stg01-mp-iad0-nc4.yaml":    "apiVersion: v1\nkind: Config",
		"not-a-yaml.txt":                  "this should be ignored",
		"config.json":                     "this should also be ignored",
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
	}

	// Create subdirectory with files to test recursive scanning
	subDir := filepath.Join(tempDir, "subdir")
	err := os.Mkdir(subDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	subFilePath := filepath.Join(subDir, "subcluster.yaml")
	err = os.WriteFile(subFilePath, []byte("apiVersion: v1\nkind: Config"), 0644)
	if err != nil {
		t.Fatalf("Failed to create subdirectory test file: %v", err)
	}

	// Set up test config
	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	// Create MultiClusterManager
	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig: staticConfig,
		clusters:     make(map[string]*Manager),
		logger:       logger,
	}

	// Test scanning
	clusters, err := mcm.scanKubeConfigDirectory()
	if err != nil {
		t.Fatalf("scanKubeConfigDirectory failed: %v", err)
	}

	// Expected clusters (should find all .yaml and .yml files)
	expectedClusters := map[string]bool{
		"cluster1":                   true,
		"cluster2":                   true,
		"stork-qa01-dp-npe-iad0-nc1": true,
		"stork-qa01-mp-npe-iad0-nc1": true,
		"stork-stg01-dp-iad0-nc4":    true,
		"stork-stg01-mp-iad0-nc4":    true,
		"subcluster":                 true, // from subdirectory
	}

	if len(clusters) != len(expectedClusters) {
		t.Errorf("Expected %d clusters, but found %d", len(expectedClusters), len(clusters))
		t.Logf("Found clusters: %v", clusters)
	}

	for clusterName := range expectedClusters {
		if _, found := clusters[clusterName]; !found {
			t.Errorf("Expected cluster %s not found", clusterName)
		}
	}

	// Verify paths are correct
	for name, path := range clusters {
		if !filepath.IsAbs(path) {
			t.Errorf("Cluster %s path should be absolute, got: %s", name, path)
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Cluster %s path does not exist: %s", name, path)
		}
	}
}

func TestScanKubeConfigDirectoryEmpty(t *testing.T) {
	// Test with empty directory
	tempDir := t.TempDir()

	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig: staticConfig,
		clusters:     make(map[string]*Manager),
		logger:       logger,
	}

	clusters, err := mcm.scanKubeConfigDirectory()
	if err != nil {
		t.Fatalf("scanKubeConfigDirectory failed: %v", err)
	}

	if len(clusters) != 0 {
		t.Errorf("Expected 0 clusters in empty directory, but found %d", len(clusters))
	}
}

func TestScanKubeConfigDirectoryNotSet(t *testing.T) {
	// Test with no directory set
	staticConfig := &config.StaticConfig{
		KubeConfigDir: "",
	}

	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig: staticConfig,
		clusters:     make(map[string]*Manager),
		logger:       logger,
	}

	clusters, err := mcm.scanKubeConfigDirectory()
	if err != nil {
		t.Fatalf("scanKubeConfigDirectory failed: %v", err)
	}

	if len(clusters) != 0 {
		t.Errorf("Expected 0 clusters when no directory set, but found %d", len(clusters))
	}
}

func TestScanKubeConfigDirectoryNonexistent(t *testing.T) {
	// Test with nonexistent directory
	staticConfig := &config.StaticConfig{
		KubeConfigDir: "/nonexistent/path",
	}

	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig: staticConfig,
		clusters:     make(map[string]*Manager),
		logger:       logger,
	}

	clusters, err := mcm.scanKubeConfigDirectory()
	if err == nil {
		t.Errorf("Expected error for nonexistent directory, but got none")
	}

	if len(clusters) != 0 {
		t.Errorf("Expected 0 clusters for nonexistent directory, but found %d", len(clusters))
	}
}

func TestApplyClusterAliases(t *testing.T) {
	staticConfig := &config.StaticConfig{
		ClusterAliases: map[string]string{
			"qa":          "stork-qa01-dp-npe-iad0-nc1",
			"staging":     "stork-stg01-dp-iad0-nc4",
			"nonexistent": "missing-cluster",
		},
	}

	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig: staticConfig,
		clusters:     make(map[string]*Manager),
		logger:       logger,
	}

	originalClusters := map[string]string{
		"stork-qa01-dp-npe-iad0-nc1": "/path/to/qa.yaml",
		"stork-stg01-dp-iad0-nc4":    "/path/to/staging.yaml",
		"other-cluster":              "/path/to/other.yaml",
	}

	aliasedClusters := mcm.applyClusterAliases(originalClusters)

	// Should have original clusters plus aliases
	expected := map[string]string{
		"stork-qa01-dp-npe-iad0-nc1": "/path/to/qa.yaml",
		"stork-stg01-dp-iad0-nc4":    "/path/to/staging.yaml",
		"other-cluster":              "/path/to/other.yaml",
		"qa":                         "/path/to/qa.yaml",      // alias
		"staging":                    "/path/to/staging.yaml", // alias
		// "nonexistent" should not be added because target doesn't exist
	}

	if len(aliasedClusters) != len(expected) {
		t.Errorf("Expected %d clusters after aliases, but got %d", len(expected), len(aliasedClusters))
	}

	for name, expectedPath := range expected {
		if actualPath, exists := aliasedClusters[name]; !exists {
			t.Errorf("Expected cluster %s not found after applying aliases", name)
		} else if actualPath != expectedPath {
			t.Errorf("Cluster %s has wrong path: expected %s, got %s", name, expectedPath, actualPath)
		}
	}
}

func TestDiscoverClustersIntegration(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create test kubeconfig files with minimal valid content
	testFiles := map[string]string{
		"stork-qa01-dp-npe-iad0-nc1.yaml": `apiVersion: v1
kind: Config
current-context: default
contexts:
- context:
    cluster: default
    user: default
  name: default
clusters:
- cluster:
    server: https://qa-cluster.example.com
  name: default
users:
- name: default
  user:
    token: fake-token
`,
		"stork-qa01-mp-npe-iad0-nc1.yaml": `apiVersion: v1
kind: Config
current-context: default
contexts:
- context:
    cluster: default
    user: default
  name: default
clusters:
- cluster:
    server: https://mp-cluster.example.com
  name: default
users:
- name: default
  user:
    token: fake-token
`,
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
	}

	// Set up test config
	staticConfig := &config.StaticConfig{
		KubeConfigDir: tempDir,
	}

	// Create MultiClusterManager (without actually connecting to clusters)
	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig: staticConfig,
		clusters:     make(map[string]*Manager),
		logger:       logger,
	}

	// Test discovery - this will fail on createClusterManager but should log the discovery part
	ctx := context.Background()
	err := mcm.DiscoverClusters(ctx)

	// We expect this to fail because we can't actually connect to fake clusters,
	// but it should discover the files correctly
	t.Logf("DiscoverClusters result: %v", err)

	// The scanning should have worked even if cluster creation failed
	discoveredClusters, scanErr := mcm.scanKubeConfigDirectory()
	if scanErr != nil {
		t.Fatalf("Scan failed: %v", scanErr)
	}

	expectedClusters := []string{
		"stork-qa01-dp-npe-iad0-nc1",
		"stork-qa01-mp-npe-iad0-nc1",
	}

	if len(discoveredClusters) != len(expectedClusters) {
		t.Errorf("Expected %d clusters, found %d", len(expectedClusters), len(discoveredClusters))
	}

	for _, expected := range expectedClusters {
		if _, found := discoveredClusters[expected]; !found {
			t.Errorf("Expected cluster %s not discovered", expected)
		}
	}
}

func TestSwitchCluster(t *testing.T) {
	// Create test clusters
	clusters := map[string]*Manager{
		"cluster1": &Manager{
			staticConfig: &config.StaticConfig{
				KubeConfig: "/path/to/cluster1.yaml",
			},
		},
		"cluster2": &Manager{
			staticConfig: &config.StaticConfig{
				KubeConfig: "/path/to/cluster2.yaml",
			},
		},
	}

	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig:  &config.StaticConfig{},
		clusters:      clusters,
		activeCluster: "cluster1",
		logger:        logger,
	}

	// Test switching to existing cluster
	err := mcm.SwitchCluster("cluster2")
	if err != nil {
		t.Errorf("SwitchCluster failed: %v", err)
	}

	if mcm.GetActiveCluster() != "cluster2" {
		t.Errorf("Expected active cluster to be cluster2, got %s", mcm.GetActiveCluster())
	}

	// Test switching to non-existent cluster
	err = mcm.SwitchCluster("nonexistent")
	if err == nil {
		t.Errorf("Expected error when switching to non-existent cluster")
	}

	// Active cluster should remain unchanged after failed switch
	if mcm.GetActiveCluster() != "cluster2" {
		t.Errorf("Active cluster should remain cluster2 after failed switch, got %s", mcm.GetActiveCluster())
	}
}

func TestSwitchClusterValidation(t *testing.T) {
	logger := klog.NewKlogr()

	// Test with nil manager
	mcm := &MultiClusterManager{
		staticConfig: &config.StaticConfig{},
		clusters: map[string]*Manager{
			"invalid": nil,
		},
		logger: logger,
	}

	err := mcm.SwitchCluster("invalid")
	if err == nil {
		t.Errorf("Expected error when switching to cluster with nil manager")
	}

	// Test with manager having nil config
	mcm.clusters["invalid"] = &Manager{
		staticConfig: nil,
	}

	err = mcm.SwitchCluster("invalid")
	if err == nil {
		t.Errorf("Expected error when switching to cluster with nil config")
	}

	// Test with manager having empty kubeconfig
	mcm.clusters["invalid"] = &Manager{
		staticConfig: &config.StaticConfig{
			KubeConfig: "",
		},
	}

	err = mcm.SwitchCluster("invalid")
	if err == nil {
		t.Errorf("Expected error when switching to cluster with empty kubeconfig")
	}
}

func TestGetActiveManager(t *testing.T) {
	clusters := map[string]*Manager{
		"cluster1": &Manager{
			staticConfig: &config.StaticConfig{
				KubeConfig: "/path/to/cluster1.yaml",
			},
		},
		"cluster2": &Manager{
			staticConfig: &config.StaticConfig{
				KubeConfig: "/path/to/cluster2.yaml",
			},
		},
	}

	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig:  &config.StaticConfig{},
		clusters:      clusters,
		activeCluster: "cluster1",
		logger:        logger,
	}

	// Test getting active manager
	manager, err := mcm.GetActiveManager()
	if err != nil {
		t.Errorf("GetActiveManager failed: %v", err)
	}

	if manager != clusters["cluster1"] {
		t.Errorf("Expected manager for cluster1, got different manager")
	}

	// Test with no active cluster
	mcm.activeCluster = ""
	_, err = mcm.GetActiveManager()
	if err == nil {
		t.Errorf("Expected error when no active cluster is set")
	}

	// Test with active cluster not found
	mcm.activeCluster = "nonexistent"
	_, err = mcm.GetActiveManager()
	if err == nil {
		t.Errorf("Expected error when active cluster doesn't exist")
	}
}

func TestConcurrentClusterSwitching(t *testing.T) {
	clusters := map[string]*Manager{
		"cluster1": &Manager{
			staticConfig: &config.StaticConfig{KubeConfig: "/path/to/cluster1.yaml"},
		},
		"cluster2": &Manager{
			staticConfig: &config.StaticConfig{KubeConfig: "/path/to/cluster2.yaml"},
		},
		"cluster3": &Manager{
			staticConfig: &config.StaticConfig{KubeConfig: "/path/to/cluster3.yaml"},
		},
	}

	logger := klog.NewKlogr()
	mcm := &MultiClusterManager{
		staticConfig:  &config.StaticConfig{},
		clusters:      clusters,
		activeCluster: "cluster1",
		logger:        logger,
	}

	const numGoroutines = 10
	const numSwitches = 100

	// Run concurrent cluster switches
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			for j := 0; j < numSwitches; j++ {
				clusterName := fmt.Sprintf("cluster%d", (j%3)+1)
				err := mcm.SwitchCluster(clusterName)
				if err != nil {
					t.Errorf("Goroutine %d: SwitchCluster failed: %v", id, err)
					return
				}

				// Verify we can get the active manager
				_, err = mcm.GetActiveManager()
				if err != nil {
					t.Errorf("Goroutine %d: GetActiveManager failed: %v", id, err)
					return
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify final state is consistent
	activeCluster := mcm.GetActiveCluster()
	if activeCluster == "" {
		t.Errorf("Active cluster should not be empty after concurrent switching")
	}

	manager, err := mcm.GetActiveManager()
	if err != nil {
		t.Errorf("Should be able to get active manager after concurrent switching: %v", err)
	}

	if manager == nil {
		t.Errorf("Active manager should not be nil")
	}
}
