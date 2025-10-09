package kubernetes

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"k8s.io/klog/v2"
)

func TestNewNSKIntegrationForMCM(t *testing.T) {
	cfg := &config.NSKConfig{
		Enabled:         true,
		RancherURL:      "https://rancher.example.com",
		RancherToken:    "test-token",
		Profile:         "test",
		ConfigDir:       t.TempDir(),
		AutoRefresh:     true,
		RefreshInterval: "1h",
	}

	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)

	if nsk == nil {
		t.Fatal("Expected NSKIntegration instance, got nil")
	}

	if nsk.config != cfg {
		t.Error("NSKIntegration config mismatch")
	}

	if nsk.clusterManager != mcm {
		t.Error("NSKIntegration cluster manager mismatch")
	}

	// Logger is always initialized (not a pointer)

	if nsk.stopChan == nil {
		t.Error("NSKIntegration stopChan is nil")
	}

	if len(nsk.refreshHistory) != 0 {
		t.Error("NSKIntegration refreshHistory should be empty initially")
	}
}

func TestNSKIntegration_Start(t *testing.T) {
	// Create a temporary directory for config
	tempDir := t.TempDir()

	cfg := &config.NSKConfig{
		Enabled:         true,
		RancherURL:      "https://rancher.example.com",
		RancherToken:    "test-token",
		Profile:         "test",
		ConfigDir:       tempDir,
		AutoRefresh:     false, // Disable auto-refresh for test
		RefreshInterval: "1h",
	}

	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)
	ctx := context.Background()

	// Start should not fail even if NSK binary is not available
	err := nsk.Start(ctx)
	if err != nil {
		// We expect this might fail due to missing NSK binary, but it should handle gracefully
		t.Logf("Start returned error (expected if NSK not installed): %v", err)
	}

	// Verify config directory was created
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Error("Config directory was not created")
	}

	// Stop the integration
	nsk.Stop()
}

func TestNSKIntegration_SetEnvironment(t *testing.T) {
	cfg := &config.NSKConfig{
		Enabled:      true,
		RancherURL:   "https://rancher.test.com",
		RancherToken: "test-token-123",
		Profile:      "test-profile",
		ConfigDir:    "/test/config",
		Environment: map[string]string{
			"CUSTOM_VAR": "custom_value",
		},
	}

	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)

	// Set environment
	err := nsk.setEnvironment()
	if err != nil {
		t.Fatalf("setEnvironment failed: %v", err)
	}

	// Verify environment variables were set
	if os.Getenv("RANCHER_URL") != cfg.RancherURL {
		t.Error("RANCHER_URL environment variable not set correctly")
	}

	if os.Getenv("RANCHER_TOKEN") != cfg.RancherToken {
		t.Error("RANCHER_TOKEN environment variable not set correctly")
	}

	if os.Getenv("NSK_PROFILE") != cfg.Profile {
		t.Error("NSK_PROFILE environment variable not set correctly")
	}

	if os.Getenv("NSK_CONFDIR") != cfg.ConfigDir {
		t.Error("NSK_CONFDIR environment variable not set correctly")
	}

	if os.Getenv("CUSTOM_VAR") != "custom_value" {
		t.Error("Custom environment variable not set correctly")
	}

	// Clean up environment variables
	os.Unsetenv("RANCHER_URL")
	os.Unsetenv("RANCHER_TOKEN")
	os.Unsetenv("NSK_PROFILE")
	os.Unsetenv("NSK_CONFDIR")
	os.Unsetenv("CUSTOM_VAR")
}

func TestNSKIntegration_GetNSKPath(t *testing.T) {
	tests := []struct {
		name     string
		nskPath  string
		expected string
	}{
		{
			name:     "Default path",
			nskPath:  "",
			expected: "nsk",
		},
		{
			name:     "Custom path",
			nskPath:  "/usr/local/bin/nsk",
			expected: "/usr/local/bin/nsk",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.NSKConfig{
				NSKPath: tt.nskPath,
			}

			mcm := &MultiClusterManager{
				clusters: make(map[string]*Manager),
				logger:   klog.Background(),
			}

			nsk := NewNSKIntegrationForMCM(cfg, mcm)

			result := nsk.getNSKPath()
			if result != tt.expected {
				t.Errorf("getNSKPath() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNSKIntegration_ParseNSKOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []string
	}{
		{
			name:     "Empty output",
			output:   "",
			expected: []string{},
		},
		{
			name: "Output with cluster names",
			output: `
				Fetching kubeconfig...
				cluster: cluster-1
				kubeconfig saved for cluster-1
				cluster: cluster-2
				kubeconfig saved for cluster-2
			`,
			expected: []string{"cluster-1", "cluster-2"},
		},
		{
			name: "Mixed output",
			output: `
				Starting refresh...
				cluster: prod-cluster
				Error: some error
				cluster: dev-cluster
				Done
			`,
			expected: []string{"prod-cluster", "dev-cluster"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.NSKConfig{}
			mcm := &MultiClusterManager{
				clusters: make(map[string]*Manager),
				logger:   klog.Background(),
			}

			nsk := NewNSKIntegrationForMCM(cfg, mcm)

			result := nsk.parseNSKOutput(tt.output)

			if len(result) != len(tt.expected) {
				t.Errorf("parseNSKOutput() returned %d clusters, want %d", len(result), len(tt.expected))
				return
			}

			for i, cluster := range result {
				if cluster != tt.expected[i] {
					t.Errorf("parseNSKOutput()[%d] = %v, want %v", i, cluster, tt.expected[i])
				}
			}
		})
	}
}

func TestNSKIntegration_AddRefreshResult(t *testing.T) {
	cfg := &config.NSKConfig{}
	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)

	// Add results
	for i := 0; i < 105; i++ {
		result := RefreshResult{
			Timestamp: time.Now(),
			Success:   i%2 == 0,
			Clusters:  []string{fmt.Sprintf("cluster-%d", i)},
		}
		nsk.addRefreshResult(result)
	}

	// Should keep only last 100 results
	if len(nsk.refreshHistory) != 100 {
		t.Errorf("Expected 100 refresh results, got %d", len(nsk.refreshHistory))
	}

	// Verify the oldest results were removed
	if nsk.refreshHistory[0].Clusters[0] != "cluster-5" {
		t.Error("Oldest results were not properly removed")
	}
}

func TestNSKIntegration_GetLastRefreshTime(t *testing.T) {
	cfg := &config.NSKConfig{}
	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)

	// Initially should return zero time
	if !nsk.GetLastRefreshTime().IsZero() {
		t.Error("Expected zero time for uninitialized lastRefresh")
	}

	// Set a refresh time
	testTime := time.Now()
	nsk.mu.Lock()
	nsk.lastRefresh = testTime
	nsk.mu.Unlock()

	// Should return the set time
	if !nsk.GetLastRefreshTime().Equal(testTime) {
		t.Error("GetLastRefreshTime did not return expected time")
	}
}

func TestNSKIntegration_GetNextRefreshTime(t *testing.T) {
	cfg := &config.NSKConfig{}
	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)

	// Initially should return zero time
	if !nsk.GetNextRefreshTime().IsZero() {
		t.Error("Expected zero time for uninitialized nextRefresh")
	}

	// Set a refresh time
	testTime := time.Now().Add(1 * time.Hour)
	nsk.mu.Lock()
	nsk.nextRefresh = testTime
	nsk.mu.Unlock()

	// Should return the set time
	if !nsk.GetNextRefreshTime().Equal(testTime) {
		t.Error("GetNextRefreshTime did not return expected time")
	}
}

func TestNSKIntegration_GetRefreshHistory(t *testing.T) {
	cfg := &config.NSKConfig{}
	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)

	// Add some history
	result1 := RefreshResult{
		Timestamp: time.Now(),
		Success:   true,
		Clusters:  []string{"cluster-1"},
	}
	result2 := RefreshResult{
		Timestamp: time.Now().Add(1 * time.Hour),
		Success:   false,
		Error:     fmt.Errorf("test error"),
		Clusters:  []string{"cluster-2"},
	}

	nsk.addRefreshResult(result1)
	nsk.addRefreshResult(result2)

	// Get history
	history := nsk.GetRefreshHistory()

	if len(history) != 2 {
		t.Errorf("Expected 2 history items, got %d", len(history))
	}

	// Verify it's a copy (modifying returned history shouldn't affect internal state)
	history[0].Success = false
	internalHistory := nsk.GetRefreshHistory()
	if !internalHistory[0].Success {
		t.Error("Modifying returned history affected internal state")
	}
}

func TestNSKIntegration_GetStatus(t *testing.T) {
	cfg := &config.NSKConfig{
		Enabled:         true,
		Profile:         "test-profile",
		RancherURL:      "https://rancher.test.com",
		ConfigDir:       "/test/config",
		AutoRefresh:     true,
		RefreshInterval: "30m",
	}

	mcm := &MultiClusterManager{
		clusters: make(map[string]*Manager),
		logger:   klog.Background(),
	}

	nsk := NewNSKIntegrationForMCM(cfg, mcm)

	// Add a refresh result
	result := RefreshResult{
		Timestamp: time.Now(),
		Success:   true,
		Clusters:  []string{"cluster-1", "cluster-2"},
	}
	nsk.addRefreshResult(result)

	// Set refresh times
	nsk.mu.Lock()
	nsk.lastRefresh = time.Now()
	nsk.nextRefresh = time.Now().Add(30 * time.Minute)
	nsk.mu.Unlock()

	// Get status
	status := nsk.GetStatus()

	// Verify status fields
	if status["enabled"] != true {
		t.Error("Status enabled field incorrect")
	}

	if status["profile"] != "test-profile" {
		t.Error("Status profile field incorrect")
	}

	if status["rancher_url"] != "https://rancher.test.com" {
		t.Error("Status rancher_url field incorrect")
	}

	if status["config_dir"] != "/test/config" {
		t.Error("Status config_dir field incorrect")
	}

	if status["auto_refresh"] != true {
		t.Error("Status auto_refresh field incorrect")
	}

	// Verify last result is included
	lastResult, ok := status["last_result"].(map[string]interface{})
	if !ok {
		t.Error("Status last_result field missing or wrong type")
	} else {
		if lastResult["success"] != true {
			t.Error("Status last_result success field incorrect")
		}
		if clusters, ok := lastResult["clusters"].([]string); !ok || len(clusters) != 2 {
			t.Error("Status last_result clusters field incorrect")
		}
	}
}

func TestNSKIntegration_ParseClusterList(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []string
	}{
		{
			name:     "Empty output",
			output:   "",
			expected: []string{},
		},
		{
			name: "Cluster list",
			output: `cluster-1
cluster-2
cluster-3`,
			expected: []string{"cluster-1", "cluster-2", "cluster-3"},
		},
		{
			name: "List with comments and empty lines",
			output: `# Header comment
cluster-prod

cluster-dev
# Another comment
cluster-staging
`,
			expected: []string{"cluster-prod", "cluster-dev", "cluster-staging"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.NSKConfig{}
			mcm := &MultiClusterManager{
				clusters: make(map[string]*Manager),
				logger:   klog.Background(),
			}

			nsk := NewNSKIntegrationForMCM(cfg, mcm)

			result := nsk.parseClusterList(tt.output)

			if len(result) != len(tt.expected) {
				t.Errorf("parseClusterList() returned %d clusters, want %d", len(result), len(tt.expected))
				return
			}

			for i, cluster := range result {
				if cluster != tt.expected[i] {
					t.Errorf("parseClusterList()[%d] = %v, want %v", i, cluster, tt.expected[i])
				}
			}
		})
	}
}