package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/mcp/jobs"
)

// initRancher initializes Rancher-specific tools
func (s *Server) initRancher() []server.ServerTool {
	// TODO: Update Rancher integration to work with SimpleMultiClusterManager
	// Temporarily disabled while using simplified multi-cluster manager
	return []server.ServerTool{}

	// Only return Rancher tools if Rancher integration is enabled
	// if s.rancher == nil {
	//	return []server.ServerTool{}
	// }

	return []server.ServerTool{
		{Tool: mcp.NewTool("rancher_list_clusters",
			mcp.WithDescription("List all clusters available in Rancher"),
			// Tool annotations
			mcp.WithTitleAnnotation("Rancher: List Clusters"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.rancherListClusters},

		{Tool: mcp.NewTool("rancher_download_cluster",
			mcp.WithDescription("Download kubeconfig for a specific cluster from Rancher"),
			mcp.WithString("cluster_name", mcp.Description("Name of the cluster to download"), mcp.Required()),
			// Tool annotations
			mcp.WithTitleAnnotation("Rancher: Download Cluster"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.rancherDownloadCluster},

		{Tool: mcp.NewTool("rancher_download_all",
			mcp.WithDescription("Download kubeconfigs for ALL clusters from Rancher (returns job_id for async tracking)"),
			// Tool annotations
			mcp.WithTitleAnnotation("Rancher: Download All Clusters (Async)"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.rancherDownloadAllAsync},

		{Tool: mcp.NewTool("rancher_status",
			mcp.WithDescription("Get status of all Kubernetes clusters in Rancher"),
			// Tool annotations
			mcp.WithTitleAnnotation("Rancher: Cluster Status"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.rancherClusterStatus},

		{Tool: mcp.NewTool("rancher_integration_status",
			mcp.WithDescription("Get Rancher integration configuration status"),
			// Tool annotations
			mcp.WithTitleAnnotation("Rancher: Integration Status"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.rancherIntegrationStatus},
	}
}

// rancherListClusters handles the rancher_list_clusters tool
func (s *Server) rancherListClusters(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	clusterNames, err := s.rancher.ListClusters(ctx)
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to list clusters: %w", err)), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d clusters in Rancher:\n\n", len(clusterNames)))

	for i, name := range clusterNames {
		result.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
	}

	return NewTextResult(result.String(), nil), nil
}

// rancherDownloadCluster handles the rancher_download_cluster tool
func (s *Server) rancherDownloadCluster(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	clusterName, _ := args["cluster_name"].(string)

	if clusterName == "" {
		return NewTextResult("", fmt.Errorf("cluster_name is required")), nil
	}

	configDir := s.configuration.StaticConfig.KubeConfigDir
	if err := s.rancher.DownloadKubeconfig(ctx, clusterName, configDir); err != nil {
		return NewTextResult("", fmt.Errorf("failed to download kubeconfig: %w", err)), nil
	}

	// Refresh cluster manager to pick up new cluster
	if err := s.clusterManager.RefreshClusters(ctx); err != nil {
		return NewTextResult("", fmt.Errorf("downloaded but failed to refresh cluster manager: %w", err)), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Successfully downloaded kubeconfig for cluster: %s\n", clusterName))
	result.WriteString(fmt.Sprintf("Saved to: %s/%s.yaml\n", configDir, clusterName))
	result.WriteString("Cluster is now available for use\n")

	return NewTextResult(result.String(), nil), nil
}

// rancherDownloadAllAsync handles the rancher_download_all tool using async jobs
func (s *Server) rancherDownloadAllAsync(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	configDir := s.configuration.StaticConfig.KubeConfigDir

	// Use static key to prevent concurrent downloads while allowing re-downloads after completion
	idempotencyKey := "rancher-download-active"

	// Create or get existing job
	job, created := s.jobManager.CreateOrGet(idempotencyKey, jobs.JobTypeRancherDownload, nil)

	// Start job if newly created
	if created {
		// TODO: Update to work with SimpleMultiClusterManager
		// executor := jobs.NewRancherDownloadExecutor(s.rancher, configDir, s.clusterManager)
		// if err := s.jobManager.StartJob(job.ID, executor); err != nil {
		//	return NewTextResult("", fmt.Errorf("failed to start job: %w", err)), nil
		// }
		return NewTextResult("", fmt.Errorf("rancher integration temporarily disabled")), nil
	}

	message := "created"
	if !created {
		message = "already exists"
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("✅ Rancher download job %s\n\n", message))
	result.WriteString(fmt.Sprintf("Job ID: %s\n", job.ID))
	result.WriteString(fmt.Sprintf("Config Directory: %s\n\n", configDir))
	result.WriteString("Use these tools to track progress:\n")
	result.WriteString(fmt.Sprintf("  • get_job_status(job_id=\"%s\") - Check progress\n", job.ID))
	result.WriteString(fmt.Sprintf("  • get_job_results(job_id=\"%s\") - View detailed results\n", job.ID))
	result.WriteString(fmt.Sprintf("  • cancel_job(job_id=\"%s\") - Cancel if needed\n", job.ID))

	return NewTextResult(result.String(), nil), nil
}

// rancherClusterStatus handles the rancher_status tool - shows status of all clusters
func (s *Server) rancherClusterStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get all clusters with their status from Rancher API
	clusters, err := s.rancher.GetClustersWithStatus(ctx)
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get cluster status: %w", err)), nil
	}

	var result strings.Builder
	result.WriteString("=== Rancher Cluster Status ===\n\n")
	result.WriteString(fmt.Sprintf("Total Clusters: %d\n\n", len(clusters)))

	// Group clusters by state
	stateGroups := make(map[string][]string)
	for _, cluster := range clusters {
		state := cluster["state"]
		name := cluster["name"]
		stateGroups[state] = append(stateGroups[state], name)
	}

	// Display clusters grouped by state
	stateOrder := []string{"active", "provisioning", "updating", "unavailable", "error"}
	stateIcons := map[string]string{
		"active":       "✅",
		"provisioning": "🔄",
		"updating":     "🔄",
		"unavailable":  "❌",
		"error":        "❌",
	}

	for _, state := range stateOrder {
		if names, ok := stateGroups[state]; ok && len(names) > 0 {
			icon := stateIcons[state]
			if icon == "" {
				icon = "❓"
			}
			result.WriteString(fmt.Sprintf("%s %s (%d):\n", icon, strings.ToUpper(state), len(names)))
			for _, name := range names {
				result.WriteString(fmt.Sprintf("  • %s\n", name))
			}
			result.WriteString("\n")
		}
	}

	// Show any other states not in our predefined list
	for state, names := range stateGroups {
		found := false
		for _, s := range stateOrder {
			if s == state {
				found = true
				break
			}
		}
		if !found && len(names) > 0 {
			result.WriteString(fmt.Sprintf("❓ %s (%d):\n", strings.ToUpper(state), len(names)))
			for _, name := range names {
				result.WriteString(fmt.Sprintf("  • %s\n", name))
			}
			result.WriteString("\n")
		}
	}

	return NewTextResult(result.String(), nil), nil
}

// rancherIntegrationStatus handles the rancher_integration_status tool
func (s *Server) rancherIntegrationStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status := s.rancher.GetStatus()

	var result strings.Builder
	result.WriteString("=== Rancher Integration Status ===\n\n")
	result.WriteString(fmt.Sprintf("Enabled: %v\n", status["enabled"]))
	result.WriteString(fmt.Sprintf("Rancher URL: %v\n", status["rancher_url"]))
	result.WriteString(fmt.Sprintf("Config Directory: %v\n", status["config_dir"]))
	result.WriteString(fmt.Sprintf("Last Refresh: %v\n", status["last_refresh"]))
	result.WriteString(fmt.Sprintf("Cluster Count: %v\n", status["cluster_count"]))

	return NewTextResult(result.String(), nil), nil
}
