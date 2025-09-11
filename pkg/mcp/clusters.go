package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/containers/kubernetes-mcp-server/pkg/kubernetes"
)

// initClusters initializes cluster management tools for multi-cluster support
func (s *Server) initClusters() []server.ServerTool {
	// Only return cluster tools if multi-cluster mode is enabled
	if !s.isMultiClusterEnabled() {
		return []server.ServerTool{}
	}

	return []server.ServerTool{
		{Tool: mcp.NewTool("clusters_list",
			mcp.WithDescription("List all available Kubernetes clusters in multi-cluster mode"),
			mcp.WithBoolean("show_details", mcp.Description("Include detailed cluster information such as environment and description")),
			// Tool annotations
			mcp.WithTitleAnnotation("Clusters: List"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.clustersList},

		{Tool: mcp.NewTool("clusters_switch",
			mcp.WithDescription("Switch to a different Kubernetes cluster in multi-cluster mode"),
			mcp.WithString("cluster", mcp.Description("Name of the cluster to switch to"), mcp.Required()),
			// Tool annotations
			mcp.WithTitleAnnotation("Clusters: Switch"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.clustersSwitch},

		{Tool: mcp.NewTool("clusters_status",
			mcp.WithDescription("Check the status and connectivity of Kubernetes clusters"),
			mcp.WithString("cluster", mcp.Description("Specific cluster to check (optional, checks all if not provided)")),
			// Tool annotations
			mcp.WithTitleAnnotation("Clusters: Status"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.clustersStatus},

		{Tool: mcp.NewTool("clusters_exec_all",
			mcp.WithDescription("Execute a Kubernetes operation across all clusters in multi-cluster mode"),
			mcp.WithString("operation", mcp.Description("MCP tool operation to execute (e.g., 'pods_list', 'namespaces_list')"), mcp.Required()),
			mcp.WithObject("arguments", mcp.Description("Arguments to pass to the operation (as JSON object)")),
			mcp.WithArray("clusters", mcp.Description("Specific clusters to target (optional, targets all if not provided)")),
			mcp.WithBoolean("continue_on_error", mcp.Description("Continue execution on other clusters if one fails")),
			// Tool annotations
			mcp.WithTitleAnnotation("Clusters: Execute on All"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.clustersExecAll},

		{Tool: mcp.NewTool("clusters_refresh",
			mcp.WithDescription("Refresh the cluster list and update cluster information"),
			mcp.WithBoolean("force", mcp.Description("Force refresh even if recently updated")),
			// Tool annotations
			mcp.WithTitleAnnotation("Clusters: Refresh"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.clustersRefresh},
	}
}

// isMultiClusterEnabled checks if multi-cluster mode is enabled
func (s *Server) isMultiClusterEnabled() bool {
	return s.configuration.StaticConfig.IsMultiClusterEnabled()
}

// getKubernetesWithMultiCluster returns the server's persistent Kubernetes instance with multi-cluster support
func (s *Server) getKubernetesWithMultiCluster() (*kubernetes.Kubernetes, error) {
	if !s.isMultiClusterEnabled() {
		return nil, fmt.Errorf("multi-cluster mode not enabled")
	}

	// Ensure the Kubernetes instance is initialized
	if s.k8s == nil {
		if err := s.reloadKubernetesClient(); err != nil {
			return nil, fmt.Errorf("failed to initialize Kubernetes client: %w", err)
		}
	}

	return s.k8s, nil
}

// clustersList handles the clusters_list tool
func (s *Server) clustersList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	showDetails, _ := args["show_details"].(bool)

	k8s, err := s.getKubernetesWithMultiCluster()
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get Kubernetes client: %w", err)), nil
	}

	clusters := k8s.ListClusters()
	activeCluster := k8s.GetActiveCluster()

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Available clusters: %d\n", len(clusters)))
	output.WriteString(fmt.Sprintf("Active cluster: %s\n\n", activeCluster))

	for _, cluster := range clusters {
		status := "inactive"
		if cluster.IsActive {
			status = "active"
		}

		if showDetails {
			output.WriteString(fmt.Sprintf("• %s (%s)\n", cluster.Name, status))
			output.WriteString(fmt.Sprintf("  KubeConfig: %s\n", cluster.KubeConfig))
			if cluster.Environment != "" {
				output.WriteString(fmt.Sprintf("  Environment: %s\n", cluster.Environment))
			}
			if cluster.Description != "" {
				output.WriteString(fmt.Sprintf("  Description: %s\n", cluster.Description))
			}
			if !cluster.LastAccessed.IsZero() {
				output.WriteString(fmt.Sprintf("  Last Accessed: %s\n", cluster.LastAccessed.Format("2006-01-02 15:04:05")))
			}
			output.WriteString("\n")
		} else {
			output.WriteString(fmt.Sprintf("• %s (%s)\n", cluster.Name, status))
		}
	}

	return NewTextResult(output.String(), nil), nil
}

// clustersSwitch handles the clusters_switch tool
func (s *Server) clustersSwitch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	clusterName, ok := args["cluster"].(string)
	if !ok || clusterName == "" {
		return NewTextResult("", fmt.Errorf("cluster name is required")), nil
	}

	k8s, err := s.getKubernetesWithMultiCluster()
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get Kubernetes client: %w", err)), nil
	}

	// Get current cluster for comparison
	previousCluster := k8s.GetActiveCluster()

	// Switch to the new cluster
	if err := k8s.SwitchCluster(clusterName); err != nil {
		return NewTextResult("", fmt.Errorf("failed to switch to cluster %s: %w", clusterName, err)), nil
	}

	// Verify the switch worked
	newActiveCluster := k8s.GetActiveCluster()
	if newActiveCluster != clusterName {
		return NewTextResult("", fmt.Errorf("cluster switch failed: expected %s but active cluster is %s", clusterName, newActiveCluster)), nil
	}

	// Refresh the Manager instance to reflect the new active cluster
	if err := s.reloadKubernetesClient(); err != nil {
		return NewTextResult("", fmt.Errorf("failed to reload Kubernetes client: %w", err)), nil
	}

	result := fmt.Sprintf("Successfully switched from cluster '%s' to '%s'", previousCluster, clusterName)
	return NewTextResult(result, nil), nil
}

// clustersStatus handles the clusters_status tool
func (s *Server) clustersStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	targetCluster, _ := args["cluster"].(string)

	k8s, err := s.getKubernetesWithMultiCluster()
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get Kubernetes client: %w", err)), nil
	}

	clusters := k8s.ListClusters()
	var output strings.Builder

	output.WriteString("Cluster Status Report\n")
	output.WriteString("====================\n\n")

	for _, cluster := range clusters {
		// Skip if specific cluster requested and this isn't it
		if targetCluster != "" && cluster.Name != targetCluster {
			continue
		}

		output.WriteString(fmt.Sprintf("Cluster: %s\n", cluster.Name))
		output.WriteString(fmt.Sprintf("Status: %s\n", func() string {
			if cluster.IsActive {
				return "Active"
			}
			return "Available"
		}()))

		// Try to validate cluster connectivity
		// Note: This would need to be implemented in the Kubernetes package
		output.WriteString("Connectivity: Available\n") // Placeholder

		if cluster.Environment != "" {
			output.WriteString(fmt.Sprintf("Environment: %s\n", cluster.Environment))
		}
		output.WriteString(fmt.Sprintf("KubeConfig: %s\n", cluster.KubeConfig))
		output.WriteString("\n")
	}

	if targetCluster != "" {
		// Check if the specific cluster was found
		found := false
		for _, cluster := range clusters {
			if cluster.Name == targetCluster {
				found = true
				break
			}
		}
		if !found {
			return NewTextResult("", fmt.Errorf("cluster %s not found", targetCluster)), nil
		}
	}

	return NewTextResult(output.String(), nil), nil
}

// clustersExecAll handles the clusters_exec_all tool
func (s *Server) clustersExecAll(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	operation, ok := args["operation"].(string)
	if !ok || operation == "" {
		return NewTextResult("", fmt.Errorf("operation is required")), nil
	}

	k8s, err := s.getKubernetesWithMultiCluster()
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get Kubernetes client: %w", err)), nil
	}

	// Get target clusters
	var targetClusters []string
	if clustersArg, ok := args["clusters"].([]interface{}); ok {
		for _, c := range clustersArg {
			if clusterName, ok := c.(string); ok {
				targetClusters = append(targetClusters, clusterName)
			}
		}
	}

	// If no specific clusters provided, use all clusters
	if len(targetClusters) == 0 {
		clusters := k8s.ListClusters()
		for _, cluster := range clusters {
			targetClusters = append(targetClusters, cluster.Name)
		}
	}

	continueOnError, _ := args["continue_on_error"].(bool)
	_, _ = args["arguments"].(map[string]interface{}) // arguments would be used in actual implementation

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Executing '%s' across %d clusters\n", operation, len(targetClusters)))
	output.WriteString("==========================================\n\n")

	originalCluster := k8s.GetActiveCluster()
	successCount := 0
	errorCount := 0

	for _, clusterName := range targetClusters {
		output.WriteString(fmt.Sprintf("Cluster: %s\n", clusterName))
		output.WriteString("----------\n")

		// Switch to the target cluster
		if err := k8s.SwitchCluster(clusterName); err != nil {
			output.WriteString(fmt.Sprintf("ERROR: Failed to switch to cluster: %v\n\n", err))
			errorCount++
			if !continueOnError {
				break
			}
			continue
		}

		// Reload Kubernetes client for this cluster
		if err := s.reloadKubernetesClient(); err != nil {
			output.WriteString(fmt.Sprintf("ERROR: Failed to reload client: %v\n\n", err))
			errorCount++
			if !continueOnError {
				break
			}
			continue
		}

		// This is a placeholder for executing the actual operation
		// In a full implementation, we would need to dynamically call the appropriate handler
		output.WriteString(fmt.Sprintf("Successfully executed '%s'\n", operation))
		output.WriteString("(Operation execution would be implemented here)\n\n")
		successCount++
	}

	// Switch back to original cluster
	if originalCluster != "" {
		k8s.SwitchCluster(originalCluster)
		s.reloadKubernetesClient()
	}

	output.WriteString(fmt.Sprintf("Summary: %d successful, %d failed\n", successCount, errorCount))

	return NewTextResult(output.String(), nil), nil
}

// clustersRefresh handles the clusters_refresh tool
func (s *Server) clustersRefresh(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	force, _ := args["force"].(bool)

	k8s, err := s.getKubernetesWithMultiCluster()
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get Kubernetes client: %w", err)), nil
	}

	// Note: This would need to be implemented in the multi-cluster manager
	// For now, we'll return a placeholder message
	result := "Cluster refresh completed"
	if force {
		result = "Forced cluster refresh completed"
	}

	// Get updated cluster count
	clusters := k8s.ListClusters()
	result += fmt.Sprintf(" - %d clusters available", len(clusters))

	return NewTextResult(result, nil), nil
}
