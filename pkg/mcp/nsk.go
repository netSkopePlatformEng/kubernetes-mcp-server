package mcp

import (
	"context"
	"fmt"
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

	return []server.ServerTool{
		{Tool: mcp.NewTool("nsk_refresh",
			mcp.WithDescription("Refresh kubeconfigs from Rancher using NSK tool"),
			mcp.WithString("profile", mcp.Description("NSK profile to use (e.g., 'npe', 'production')")),
			mcp.WithString("pattern", mcp.Description("Cluster name pattern to filter (optional)")),
			mcp.WithBoolean("force", mcp.Description("Force refresh even if recently updated")),
			// Tool annotations
			mcp.WithTitleAnnotation("NSK: Refresh Kubeconfigs"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.nskRefresh},

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
			mcp.WithDescription("Download kubeconfig for a specific cluster from Rancher"),
			mcp.WithString("cluster", mcp.Description("Name of the cluster to download"), mcp.Required()),
			mcp.WithString("profile", mcp.Description("NSK profile to use (e.g., 'npe', 'production')")),
			// Tool annotations
			mcp.WithTitleAnnotation("NSK: Download Cluster Config"),
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

// nskRefresh handles the nsk_refresh tool
func (s *Server) nskRefresh(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		// Check if recently refreshed
		lastRefresh := nsk.GetLastRefreshTime()
		if time.Since(lastRefresh) < 5*time.Minute {
			result.WriteString(fmt.Sprintf("Clusters were recently refreshed at %s\n", lastRefresh.Format(time.RFC3339)))
			result.WriteString("Use 'force: true' to force refresh\n")
			return NewTextResult(result.String(), nil), nil
		}
	}

	// Pattern will be passed directly to RefreshKubeConfigs
	// We can't modify the config directly as it's unexported

	// Refresh via NSK
	if err := nsk.RefreshKubeConfigs(ctx); err != nil {
		return NewTextResult("", fmt.Errorf("failed to refresh kubeconfigs via NSK: %w", err)), nil
	}

	result.WriteString("Successfully refreshed kubeconfigs from Rancher via NSK\n")

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

	// Discover clusters
	clusters, err := nsk.DiscoverClusters(ctx, pattern)
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to list clusters via NSK: %w", err)), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d clusters in Rancher:\n\n", len(clusters)))

	for _, cluster := range clusters {
		result.WriteString(fmt.Sprintf("  • %s\n", cluster))
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
	// Read NSK config from ~/.mcp/configuration if it exists
	configDir := "/Users/jdambly/.mcp"
	if s.configuration.StaticConfig != nil && s.configuration.StaticConfig.KubeConfigDir != "" {
		configDir = s.configuration.StaticConfig.KubeConfigDir
	}

	// Create minimal NSK config
	nskConfig := &config.NSKConfig{
		Enabled:   true,
		ConfigDir: configDir,
		Profile:   profile,
	}

	// If profile not specified, use "npe" as default
	if nskConfig.Profile == "" {
		nskConfig.Profile = "npe"
	}

	// Create temporary NSK integration
	if s.clusterManager != nil {
		return internalk8s.NewNSKIntegrationForMCM(nskConfig, s.clusterManager)
	}

	return nil
}
