package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	internalk8s "github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
)

// initNSK initializes NSK-specific tools
func (s *Server) initNSK() []server.ServerTool {
	// Only return NSK tools if multi-cluster mode is enabled
	if !s.isMultiClusterEnabled() {
		return []server.ServerTool{}
	}

	// Initialize NSK integration if not already configured
	if s.nsk == nil && s.configuration.StaticConfig.NSKIntegration == nil {
		// Create default NSK configuration for multi-cluster mode
		s.configuration.StaticConfig.NSKIntegration = &config.NSKConfig{
			Enabled:      true,
			ConfigDir:    s.configuration.StaticConfig.KubeConfigDir,
			Profile:      "npe",
			RancherURL:   "https://rancher.prime.iad0.netskope.com",
			RancherToken: "token-64l2k:dfnjc76lcgthfn8s4wlzqmpjsvfljvxtgvqb2224z2bmkzsrx4qszx",
		}
	}

	return []server.ServerTool{
		{Tool: mcp.NewTool("nsk_refresh",
			mcp.WithDescription("Refresh the cluster list from local kubeconfig files (does not download from Rancher)"),
			mcp.WithBoolean("force", mcp.Description("Force refresh even if recently updated")),
			// Tool annotations
			mcp.WithTitleAnnotation("NSK: Refresh Cluster List"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.nskRefresh},

		{Tool: mcp.NewTool("nsk_download_all",
			mcp.WithDescription("Download ALL kubeconfigs from Rancher using NSK tool (fetches fresh tokens from Rancher)"),
			mcp.WithString("profile", mcp.Description("NSK profile to use (e.g., 'npe', 'production')")),
			mcp.WithString("pattern", mcp.Description("Cluster name pattern to filter (optional)")),
			mcp.WithBoolean("force", mcp.Description("Force download even if recently updated")),
			// Tool annotations
			mcp.WithTitleAnnotation("NSK: Download All Clusters"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.nskDownloadAll},

		{Tool: mcp.NewTool("nsk_list",
			mcp.WithDescription("List clusters available in Rancher via NSK"),
			mcp.WithString("profile", mcp.Description("NSK profile to use (e.g., 'npe', 'production')")),
			mcp.WithString("pattern", mcp.Description("Cluster name pattern to filter (optional)")),
			// Tool annotations
			mcp.WithTitleAnnotation("NSK: List Rancher Clusters"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.nskList},

		{Tool: mcp.NewTool("nsk_download",
			mcp.WithDescription("Download kubeconfig for a SINGLE specific cluster from Rancher (use nsk_download_all for all clusters)"),
			mcp.WithString("cluster", mcp.Description("Name of the specific cluster to download"), mcp.Required()),
			mcp.WithString("profile", mcp.Description("NSK profile to use (e.g., 'npe', 'production')")),
			// Tool annotations
			mcp.WithTitleAnnotation("NSK: Download Single Cluster"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.nskDownload},

		{Tool: mcp.NewTool("nsk_status",
			mcp.WithDescription("Check NSK integration status and configuration"),
			// Tool annotations
			mcp.WithTitleAnnotation("NSK: Integration Status"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.nskStatus},
	}
}

// nskDownloadAll handles the nsk_download_all tool
// This tool downloads ALL clusters from Rancher with fresh tokens
func (s *Server) nskDownloadAll(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	profile, _ := args["profile"].(string)
	// pattern, _ := args["pattern"].(string) // TODO: Pass pattern to RefreshKubeConfigs when supported
	force, _ := args["force"].(bool)

	// Create temporary NSK integration if not already initialized
	nsk := s.getOrCreateNSK(profile)
	if nsk == nil {
		return NewTextResult("", fmt.Errorf("NSK integration not available. Check NSK configuration in ~/.mcp/configuration")), nil
	}

	var result strings.Builder

	if !force {
		// Check if recently downloaded
		lastRefresh := nsk.GetLastRefreshTime()
		if time.Since(lastRefresh) < 5*time.Minute {
			result.WriteString(fmt.Sprintf("Clusters were recently downloaded from Rancher at %s\n", lastRefresh.Format(time.RFC3339)))
			result.WriteString("Use 'force: true' to force download\n")
			return NewTextResult(result.String(), nil), nil
		}
	}

	// Pattern will be passed directly to RefreshKubeConfigs
	// We can't modify the config directly as it's unexported

	// Download all kubeconfigs via NSK
	if err := nsk.RefreshKubeConfigs(ctx); err != nil {
		return NewTextResult("", fmt.Errorf("failed to download kubeconfigs from Rancher via NSK: %w", err)), nil
	}

	result.WriteString("Successfully downloaded ALL kubeconfigs from Rancher via NSK\n")

	// Refresh cluster manager
	if s.clusterManager != nil {
		if err := s.clusterManager.RefreshClusters(ctx); err != nil {
			result.WriteString(fmt.Sprintf("Warning: Failed to refresh cluster manager: %v\n", err))
		} else {
			clusters := s.clusterManager.ListClusters()
			result.WriteString(fmt.Sprintf("Loaded %d clusters\n", len(clusters)))
		}
	}

	return NewTextResult(result.String(), nil), nil
}

// nskRefresh handles the nsk_refresh tool
// This tool just reloads the cluster list from local kubeconfig files
func (s *Server) nskRefresh(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var result strings.Builder

	// Get kubeconfig directory
	kubeconfigDir := s.configuration.StaticConfig.KubeConfigDir
	if kubeconfigDir == "" {
		kubeconfigDir = "~/.mcp"
	}
	if strings.HasPrefix(kubeconfigDir, "~") {
		home, _ := os.UserHomeDir()
		kubeconfigDir = strings.Replace(kubeconfigDir, "~", home, 1)
	}

	// Check current status
	result.WriteString("📊 Refreshing cluster list from local kubeconfig files...\n\n")

	existingFiles := []string{}
	if entries, err := os.ReadDir(kubeconfigDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
				existingFiles = append(existingFiles, strings.TrimSuffix(entry.Name(), ".yaml"))
			}
		}
	}

	if len(existingFiles) > 0 {
		result.WriteString(fmt.Sprintf("✅ Found %d kubeconfig files in %s\n\n", len(existingFiles), kubeconfigDir))
	} else {
		result.WriteString(fmt.Sprintf("⚠️  No kubeconfig files found in %s\n\n", kubeconfigDir))
		result.WriteString("Use nsk_download_all to download clusters from Rancher\n")
		return NewTextResult(result.String(), nil), nil
	}

	// Refresh cluster manager with existing files
	if s.clusterManager != nil {
		if err := s.clusterManager.RefreshClusters(ctx); err != nil {
			result.WriteString(fmt.Sprintf("❌ Failed to refresh cluster manager: %v\n", err))
		} else {
			clusters := s.clusterManager.ListClusters()
			result.WriteString(fmt.Sprintf("✅ Cluster manager refreshed with %d clusters\n", len(clusters)))
		}
	}

	return NewTextResult(result.String(), nil), nil
}

// nskList handles the nsk_list tool
func (s *Server) nskList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	profile, _ := args["profile"].(string)
	pattern, _ := args["pattern"].(string)

	// Create temporary NSK integration
	nsk := s.getOrCreateNSK(profile)
	if nsk == nil {
		return NewTextResult("", fmt.Errorf("NSK integration not available. Check NSK configuration in ~/.mcp/configuration")), nil
	}

	// Use a timeout to prevent hanging
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Discover clusters
	clusters, err := nsk.DiscoverClusters(listCtx, pattern)
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to list clusters via NSK: %w", err)), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d clusters in Rancher:\n\n", len(clusters)))

	// Check local status for each cluster
	localCount := 0
	for _, cluster := range clusters {
		kubeconfigPath := fmt.Sprintf("%s/%s.yaml", s.configuration.StaticConfig.KubeConfigDir, cluster)
		if _, err := os.Stat(kubeconfigPath); err == nil {
			result.WriteString(fmt.Sprintf("  ✓ %s (local)\n", cluster))
			localCount++
		} else {
			result.WriteString(fmt.Sprintf("  ○ %s\n", cluster))
		}
	}

	result.WriteString(fmt.Sprintf("\n📊 Summary: %d total, %d downloaded locally\n", len(clusters), localCount))
	if localCount < len(clusters) {
		result.WriteString("Use nsk_download_all to download missing clusters\n")
	}

	return NewTextResult(result.String(), nil), nil
}

// nskDownload handles the nsk_download tool
func (s *Server) nskDownload(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	clusterName, _ := args["cluster"].(string)
	profile, _ := args["profile"].(string)

	if clusterName == "" {
		return NewTextResult("", fmt.Errorf("cluster name is required")), nil
	}

	// Create temporary NSK integration
	nsk := s.getOrCreateNSK(profile)
	if nsk == nil {
		return NewTextResult("", fmt.Errorf("NSK integration not available. Check NSK configuration in ~/.mcp/configuration")), nil
	}

	// Download kubeconfig for specific cluster
	_, err := nsk.GetClusterKubeConfig(ctx, clusterName, true)
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to download kubeconfig for cluster %s: %w", clusterName, err)), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Successfully downloaded kubeconfig for cluster: %s\n", clusterName))

	configDir := "~/.mcp"
	if s.configuration.StaticConfig != nil && s.configuration.StaticConfig.KubeConfigDir != "" {
		configDir = s.configuration.StaticConfig.KubeConfigDir
	}
	result.WriteString(fmt.Sprintf("Saved to: %s/%s.yaml\n", configDir, clusterName))

	// Refresh cluster manager to pick up new cluster
	if s.clusterManager != nil {
		if err := s.clusterManager.RefreshClusters(ctx); err != nil {
			result.WriteString(fmt.Sprintf("Warning: Failed to refresh cluster manager: %v\n", err))
		} else {
			result.WriteString("Cluster is now available for use\n")
		}
	}

	return NewTextResult(result.String(), nil), nil
}

// nskStatus handles the nsk_status tool
func (s *Server) nskStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var result strings.Builder

	result.WriteString("=== NSK Integration Status ===\n\n")

	// Check if NSK is configured
	if s.nsk != nil {
		result.WriteString("NSK Integration: CONFIGURED\n")
		status := s.nsk.GetStatus()

		result.WriteString(fmt.Sprintf("  • Profile: %v\n", status["profile"]))
		result.WriteString(fmt.Sprintf("  • Rancher URL: %v\n", status["rancher_url"]))
		result.WriteString(fmt.Sprintf("  • Config Directory: %v\n", status["config_dir"]))
		result.WriteString(fmt.Sprintf("  • Auto-refresh: %v\n", status["auto_refresh"]))

		if lastResult, ok := status["last_result"].(map[string]interface{}); ok {
			result.WriteString(fmt.Sprintf("  • Last Refresh: %v\n", lastResult["timestamp"]))
			result.WriteString(fmt.Sprintf("  • Last Status: %v\n", lastResult["success"]))
		}
	} else {
		result.WriteString("NSK Integration: NOT CONFIGURED\n\n")

		// Check for NSK config file
		result.WriteString("Checking for NSK configuration file...\n")
		if s.configuration.StaticConfig != nil && s.configuration.StaticConfig.KubeConfigDir != "" {
			result.WriteString(fmt.Sprintf("  • Config directory: %s\n", s.configuration.StaticConfig.KubeConfigDir))
		} else {
			result.WriteString("  • Config directory: ~/.mcp\n")
		}
		result.WriteString("  • Configuration file: ~/.mcp/configuration\n")
		result.WriteString("\nTo enable NSK, ensure ~/.mcp/configuration exists with:\n")
		result.WriteString("  [npe]\n")
		result.WriteString("  endpoint = \"https://rancher.prime.iad0.netskope.com\"\n")
		result.WriteString("  token = \"your-rancher-token\"\n")
		result.WriteString("  confdir = \"/Users/yourusername/.mcp\"\n")
	}

	return NewTextResult(result.String(), nil), nil
}

// getOrCreateNSK gets existing NSK or creates a temporary one
func (s *Server) getOrCreateNSK(profile string) *internalk8s.NSKIntegration {
	// Return existing if available
	if s.nsk != nil {
		return s.nsk
	}

	// Try to create temporary NSK integration
	configDir := "/Users/jdambly/.mcp"
	if s.configuration.StaticConfig != nil && s.configuration.StaticConfig.KubeConfigDir != "" {
		configDir = s.configuration.StaticConfig.KubeConfigDir
	}

	// If profile not specified, use "npe" as default
	if profile == "" {
		profile = "npe"
	}

	// Create NSK config with hardcoded NPE credentials for now
	// TODO: Parse ~/.mcp/configuration file to get credentials
	nskConfig := &config.NSKConfig{
		Enabled:      true,
		ConfigDir:    configDir,
		Profile:      profile,
		RancherURL:   "https://rancher.prime.iad0.netskope.com",
		RancherToken: "token-64l2k:dfnjc76lcgthfn8s4wlzqmpjsvfljvxtgvqb2224z2bmkzsrx4qszx",
	}

	// Create temporary NSK integration
	if s.clusterManager != nil {
		nsk := internalk8s.NewNSKIntegrationForMCM(nskConfig, s.clusterManager)
		// Set environment variables
		ctx := context.Background()
		nsk.Start(ctx) // This will set the environment variables
		return nsk
	}

	return nil
}
