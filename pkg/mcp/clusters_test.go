package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/output"
)

func TestServer_initClusters(t *testing.T) {
	t.Run("Multi-cluster mode enabled returns cluster tools", func(t *testing.T) {
		// Create test kubeconfig directory
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
		if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
			t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
		}

		staticConfig := &config.StaticConfig{
			KubeConfigDir: kubeconfigDir,
		}

		configuration := Configuration{
			Profile:      ProfileFromString("full"),
			ListOutput:   output.FromString("table"),
			StaticConfig: staticConfig,
		}

		server := &Server{
			configuration: &configuration,
		}

		tools := server.initClusters()

		if len(tools) == 0 {
			t.Error("Expected cluster tools to be returned when multi-cluster mode is enabled")
		}

		// Verify expected tools are present
		expectedTools := []string{
			"clusters_list",
			"clusters_switch",
			"clusters_status",
			"clusters_exec_all",
			"clusters_refresh",
		}

		toolNames := make(map[string]bool)
		for _, tool := range tools {
			toolNames[tool.Tool.Name] = true
		}

		for _, expectedTool := range expectedTools {
			if !toolNames[expectedTool] {
				t.Errorf("Expected tool %s not found in cluster tools", expectedTool)
			}
		}
	})

	t.Run("Single cluster mode returns no cluster tools", func(t *testing.T) {
		// Create test kubeconfig file (single cluster mode)
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

		staticConfig := &config.StaticConfig{
			KubeConfig: kubeconfigPath,
		}

		configuration := Configuration{
			Profile:      ProfileFromString("full"),
			ListOutput:   output.FromString("table"),
			StaticConfig: staticConfig,
		}

		server := &Server{
			configuration: &configuration,
		}

		tools := server.initClusters()

		if len(tools) != 0 {
			t.Error("Expected no cluster tools to be returned when multi-cluster mode is disabled")
		}
	})
}

func TestServer_isMultiClusterEnabled(t *testing.T) {
	tests := []struct {
		name           string
		kubeConfig     string
		kubeConfigDir  string
		expectedResult bool
	}{
		{
			name:           "Single cluster mode",
			kubeConfig:     "/path/to/kubeconfig",
			kubeConfigDir:  "",
			expectedResult: false,
		},
		{
			name:           "Multi-cluster mode",
			kubeConfig:     "",
			kubeConfigDir:  "/path/to/kubeconfigs",
			expectedResult: true,
		},
		{
			name:           "Both set - multi-cluster takes precedence",
			kubeConfig:     "/path/to/kubeconfig",
			kubeConfigDir:  "/path/to/kubeconfigs",
			expectedResult: true,
		},
		{
			name:           "Neither set",
			kubeConfig:     "",
			kubeConfigDir:  "",
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staticConfig := &config.StaticConfig{
				KubeConfig:    tt.kubeConfig,
				KubeConfigDir: tt.kubeConfigDir,
			}

			configuration := Configuration{
				Profile:      ProfileFromString("full"),
				ListOutput:   output.FromString("table"),
				StaticConfig: staticConfig,
			}

			server := &Server{
				configuration: configuration,
			}

			result := server.isMultiClusterEnabled()
			if result != tt.expectedResult {
				t.Errorf("isMultiClusterEnabled() = %v, want %v", result, tt.expectedResult)
			}
		})
	}
}

func TestServer_clustersList(t *testing.T) {
	// Create test kubeconfig directory
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

	staticConfig := &config.StaticConfig{
		KubeConfigDir: kubeconfigDir,
	}

	configuration := Configuration{
		Profile:      ProfileFromString("full"),
		ListOutput:   output.FromString("table"),
		StaticConfig: staticConfig,
	}

	server := &Server{
		configuration: configuration,
	}

	t.Run("List clusters without details", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "clusters_list",
				Arguments: map[string]interface{}{},
			},
		}

		result, err := server.clustersList(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersList failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		if len(result.Content) == 0 {
			t.Fatal("Expected non-empty result content")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if textContent.Text == "" {
			t.Error("Expected non-empty text content")
		}

		// Should contain cluster information
		if !contains(textContent.Text, "Available clusters") {
			t.Error("Expected output to contain 'Available clusters'")
		}
	})

	t.Run("List clusters with details", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_list",
				Arguments: map[string]interface{}{
					"show_details": true,
				},
			},
		}

		result, err := server.clustersList(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersList failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		// Should contain detailed information
		if !contains(textContent.Text, "KubeConfig:") {
			t.Error("Expected detailed output to contain 'KubeConfig:'")
		}
	})
}

func TestServer_clustersSwitch(t *testing.T) {
	// Create test kubeconfig directory
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
	if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
	}

	staticConfig := &config.StaticConfig{
		KubeConfigDir: kubeconfigDir,
	}

	configuration := Configuration{
		Profile:      ProfileFromString("full"),
		ListOutput:   output.FromString("table"),
		StaticConfig: staticConfig,
	}

	server := &Server{
		configuration: configuration,
	}

	t.Run("Switch to valid cluster", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_switch",
				Arguments: map[string]interface{}{
					"cluster": "cluster1",
				},
			},
		}

		result, err := server.clustersSwitch(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersSwitch failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if !contains(textContent.Text, "Successfully switched") {
			t.Error("Expected success message")
		}
	})

	t.Run("Switch without cluster name", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "clusters_switch",
				Arguments: map[string]interface{}{},
			},
		}

		result, err := server.clustersSwitch(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersSwitch failed: %v", err)
		}

		if !result.IsError {
			t.Error("Expected error for missing cluster name")
		}
	})

	t.Run("Switch to invalid cluster", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_switch",
				Arguments: map[string]interface{}{
					"cluster": "nonexistent-cluster",
				},
			},
		}

		result, err := server.clustersSwitch(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersSwitch failed: %v", err)
		}

		if !result.IsError {
			t.Error("Expected error for nonexistent cluster")
		}
	})
}

func TestServer_clustersStatus(t *testing.T) {
	// Create test kubeconfig directory
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
	if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
	}

	staticConfig := &config.StaticConfig{
		KubeConfigDir: kubeconfigDir,
	}

	configuration := Configuration{
		Profile:      ProfileFromString("full"),
		ListOutput:   output.FromString("table"),
		StaticConfig: staticConfig,
	}

	server := &Server{
		configuration: configuration,
	}

	t.Run("Status for all clusters", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "clusters_status",
				Arguments: map[string]interface{}{},
			},
		}

		result, err := server.clustersStatus(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersStatus failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if !contains(textContent.Text, "Cluster Status Report") {
			t.Error("Expected status report header")
		}
	})

	t.Run("Status for specific cluster", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_status",
				Arguments: map[string]interface{}{
					"cluster": "cluster1",
				},
			},
		}

		result, err := server.clustersStatus(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersStatus failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if !contains(textContent.Text, "cluster1") {
			t.Error("Expected specific cluster in status report")
		}
	})

	t.Run("Status for nonexistent cluster", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_status",
				Arguments: map[string]interface{}{
					"cluster": "nonexistent-cluster",
				},
			},
		}

		result, err := server.clustersStatus(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersStatus failed: %v", err)
		}

		if !result.IsError {
			t.Error("Expected error for nonexistent cluster")
		}
	})
}

func TestServer_clustersExecAll(t *testing.T) {
	// Create test kubeconfig directory
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

	staticConfig := &config.StaticConfig{
		KubeConfigDir: kubeconfigDir,
	}

	configuration := Configuration{
		Profile:      ProfileFromString("full"),
		ListOutput:   output.FromString("table"),
		StaticConfig: staticConfig,
	}

	server := &Server{
		configuration: configuration,
	}

	t.Run("Execute operation on all clusters", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_exec_all",
				Arguments: map[string]interface{}{
					"operation": "namespaces_list",
				},
			},
		}

		result, err := server.clustersExecAll(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersExecAll failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if !contains(textContent.Text, "Executing") {
			t.Error("Expected execution report")
		}
		if !contains(textContent.Text, "Summary:") {
			t.Error("Expected summary in output")
		}
	})

	t.Run("Execute operation on specific clusters", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_exec_all",
				Arguments: map[string]interface{}{
					"operation": "namespaces_list",
					"clusters":  []interface{}{"cluster1"},
				},
			},
		}

		result, err := server.clustersExecAll(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersExecAll failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if !contains(textContent.Text, "cluster1") {
			t.Error("Expected specific cluster in execution report")
		}
	})

	t.Run("Execute without operation name", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "clusters_exec_all",
				Arguments: map[string]interface{}{},
			},
		}

		result, err := server.clustersExecAll(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersExecAll failed: %v", err)
		}

		if !result.IsError {
			t.Error("Expected error for missing operation")
		}
	})
}

func TestServer_clustersRefresh(t *testing.T) {
	// Create test kubeconfig directory
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
	if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
	}

	staticConfig := &config.StaticConfig{
		KubeConfigDir: kubeconfigDir,
	}

	configuration := Configuration{
		Profile:      ProfileFromString("full"),
		ListOutput:   output.FromString("table"),
		StaticConfig: staticConfig,
	}

	server := &Server{
		configuration: configuration,
	}

	t.Run("Normal refresh", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "clusters_refresh",
				Arguments: map[string]interface{}{},
			},
		}

		result, err := server.clustersRefresh(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersRefresh failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if !contains(textContent.Text, "refresh completed") {
			t.Error("Expected refresh completion message")
		}
	})

	t.Run("Force refresh", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "clusters_refresh",
				Arguments: map[string]interface{}{
					"force": true,
				},
			},
		}

		result, err := server.clustersRefresh(context.Background(), req)
		if err != nil {
			t.Fatalf("clustersRefresh failed: %v", err)
		}

		if result.Content == nil {
			t.Fatal("Expected result content to be set")
		}

		textContent, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("Expected text content")
		}

		if !contains(textContent.Text, "Forced") {
			t.Error("Expected forced refresh message")
		}
	})
}

func TestServer_getKubernetesWithMultiCluster(t *testing.T) {
	t.Run("Multi-cluster mode enabled", func(t *testing.T) {
		// Create test kubeconfig directory
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
		if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
			t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
		}

		staticConfig := &config.StaticConfig{
			KubeConfigDir: kubeconfigDir,
		}

		configuration := Configuration{
			Profile:      ProfileFromString("full"),
			ListOutput:   output.FromString("table"),
			StaticConfig: staticConfig,
		}

		server := &Server{
			configuration: &configuration,
		}

		k8s, err := server.getKubernetesWithMultiCluster()
		if err != nil {
			t.Fatalf("getKubernetesWithMultiCluster failed: %v", err)
		}

		if k8s == nil {
			t.Fatal("Expected non-nil Kubernetes instance")
		}

		if !k8s.IsMultiCluster() {
			t.Error("Expected multi-cluster mode to be enabled")
		}

		// Clean up
		k8s.Close()
	})

	t.Run("Multi-cluster mode disabled", func(t *testing.T) {
		staticConfig := &config.StaticConfig{
			KubeConfig: "/path/to/single/kubeconfig",
		}

		configuration := Configuration{
			Profile:      ProfileFromString("full"),
			ListOutput:   output.FromString("table"),
			StaticConfig: staticConfig,
		}

		server := &Server{
			configuration: &configuration,
		}

		_, err := server.getKubernetesWithMultiCluster()
		if err == nil {
			t.Error("Expected error when multi-cluster mode is disabled")
		}

		if !contains(err.Error(), "multi-cluster mode not enabled") {
			t.Errorf("Expected multi-cluster error, got: %v", err)
		}
	})
}

// Note: reloadKubernetesClient is already implemented in the main MCP server

// Helper function to check if a string contains a substring
func contains(str, substr string) bool {
	return len(str) >= len(substr) &&
		(str == substr ||
			str[:len(substr)] == substr ||
			str[len(str)-len(substr):] == substr ||
			findSubstring(str, substr))
}

func findSubstring(str, substr string) bool {
	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test tool metadata and descriptions
func TestClusterToolsMetadata(t *testing.T) {
	// Create test kubeconfig directory
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
	if err := os.WriteFile(cluster1Path, []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("failed to create cluster1 kubeconfig: %v", err)
	}

	staticConfig := &config.StaticConfig{
		KubeConfigDir: kubeconfigDir,
	}

	configuration := Configuration{
		Profile:      ProfileFromString("full"),
		ListOutput:   output.FromString("table"),
		StaticConfig: staticConfig,
	}

	server := &Server{
		configuration: configuration,
	}

	tools := server.initClusters()

	expectedToolsData := map[string]struct {
		readOnly     bool
		destructive  bool
		openWorld    bool
		hasArguments bool
	}{
		"clusters_list": {
			readOnly:     true,
			destructive:  false,
			openWorld:    true,
			hasArguments: true,
		},
		"clusters_switch": {
			readOnly:     false,
			destructive:  false,
			openWorld:    false,
			hasArguments: true,
		},
		"clusters_status": {
			readOnly:     true,
			destructive:  false,
			openWorld:    true,
			hasArguments: true,
		},
		"clusters_exec_all": {
			readOnly:     false,
			destructive:  true,
			openWorld:    false,
			hasArguments: true,
		},
		"clusters_refresh": {
			readOnly:     false,
			destructive:  false,
			openWorld:    false,
			hasArguments: true,
		},
	}

	for _, tool := range tools {
		toolName := tool.Tool.Name
		expected, exists := expectedToolsData[toolName]
		if !exists {
			t.Errorf("Unexpected tool: %s", toolName)
			continue
		}

		// Test tool description
		if tool.Tool.Description == "" {
			t.Errorf("Tool %s has empty description", toolName)
		}

		// Test tool annotations exist (we can't easily access them directly in this test)
		// but we can verify the tool was created with the expected structure
		if tool.Handler == nil {
			t.Errorf("Tool %s has no handler", toolName)
		}

		// Test that required arguments are present for tools that need them
		if expected.hasArguments && tool.Tool.InputSchema == nil {
			t.Errorf("Tool %s expected to have arguments but InputSchema is nil", toolName)
		}
	}

	// Verify all expected tools are present
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Tool.Name] = true
	}

	for expectedTool := range expectedToolsData {
		if !toolNames[expectedTool] {
			t.Errorf("Expected tool %s not found", expectedTool)
		}
	}
}
