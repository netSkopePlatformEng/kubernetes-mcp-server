package mcp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
)

// ClusterStatus represents the status of a cluster
type ClusterStatus struct {
	Name           string
	Status         string // Ready, NotReady, Unknown
	Version        string
	NodeCount      int
	ReadyNodes     int
	NamespaceCount int
	PodCount       int
	RunningPods    int
	KubeconfigAge  time.Duration
	LastError      string
	IsActive       bool
	APILatency     time.Duration
}

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
			mcp.WithReadOnlyHintAnnotation(true),
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

		{Tool: mcp.NewTool("clusters_refresh",
			mcp.WithDescription("Refresh the cluster list and update cluster information"),
			mcp.WithBoolean("force", mcp.Description("Force refresh even if recently updated")),
			// Tool annotations
			mcp.WithTitleAnnotation("Clusters: Refresh"),
			mcp.WithReadOnlyHintAnnotation(true),
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

	if s.clusterManager == nil {
		return NewTextResult("", fmt.Errorf("cluster manager not initialized")), nil
	}

	clusters := s.clusterManager.ListClusters()
	if len(clusters) == 0 {
		return NewTextResult("No clusters found. Run 'clusters_refresh' to discover clusters.", nil), nil
	}

	var result strings.Builder

	if showDetails {
		// Detailed view
		for i, cluster := range clusters {
			if i > 0 {
				result.WriteString("\n---\n\n")
			}

			activeMarker := ""
			if cluster.IsActive {
				activeMarker = " [ACTIVE]"
			}

			result.WriteString(fmt.Sprintf("Cluster: %s%s\n", cluster.Name, activeMarker))
			result.WriteString(fmt.Sprintf("Kubeconfig: %s\n", cluster.KubeConfig))

			if cluster.Environment != "" {
				result.WriteString(fmt.Sprintf("Environment: %s\n", cluster.Environment))
			}
			if cluster.Description != "" {
				result.WriteString(fmt.Sprintf("Description: %s\n", cluster.Description))
			}
			if !cluster.LastAccessed.IsZero() {
				result.WriteString(fmt.Sprintf("Last Accessed: %s\n", cluster.LastAccessed.Format(time.RFC3339)))
			}
		}
	} else {
		// Table view
		w := tabwriter.NewWriter(&result, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tACTIVE\tENVIRONMENT")

		for _, cluster := range clusters {
			active := ""
			if cluster.IsActive {
				active = "*"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\n",
				cluster.Name,
				active,
				cluster.Environment,
			)
		}
		w.Flush()
	}

	return NewTextResult(result.String(), nil), nil
}

// clustersSwitch handles the clusters_switch tool
func (s *Server) clustersSwitch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	clusterName, ok := args["cluster"].(string)
	if !ok || clusterName == "" {
		return NewTextResult("", fmt.Errorf("cluster name is required")), nil
	}

	if s.clusterManager == nil {
		return NewTextResult("", fmt.Errorf("cluster manager not initialized")), nil
	}

	// Get current cluster for comparison
	previousCluster := s.clusterManager.GetActiveCluster()

	// Switch to the new cluster
	if err := s.clusterManager.SwitchCluster(clusterName); err != nil {
		return NewTextResult("", fmt.Errorf("failed to switch to cluster %s: %w", clusterName, err)), nil
	}

	// Verify the switch worked
	newActiveCluster := s.clusterManager.GetActiveCluster()
	if newActiveCluster != clusterName {
		return NewTextResult("", fmt.Errorf("cluster switch failed: expected %s but active cluster is %s", clusterName, newActiveCluster)), nil
	}

	// With fresh manager approach, we don't need to update s.k
	// Each operation will create its own fresh manager
	// This eliminates all state persistence issues

	result := fmt.Sprintf("Successfully switched from cluster '%s' to '%s'", previousCluster, clusterName)
	return NewTextResult(result, nil), nil
}

// clustersStatus handles the clusters_status tool
func (s *Server) clustersStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	clusterName, _ := args["cluster"].(string)

	if s.clusterManager == nil {
		return NewTextResult("", fmt.Errorf("cluster manager not initialized")), nil
	}

	var statuses []ClusterStatus

	if clusterName != "" {
		// Check specific cluster
		status := s.checkClusterStatus(ctx, clusterName)
		statuses = append(statuses, status)

		// Detailed view for single cluster
		return s.formatDetailedClusterStatus(status), nil
	}

	// Check all clusters
	clusters := s.clusterManager.ListClusters()
	for _, cluster := range clusters {
		status := s.checkClusterStatus(ctx, cluster.Name)
		statuses = append(statuses, status)
	}

	// Format as table
	return s.formatClusterStatusTable(statuses), nil
}

// checkClusterStatus checks the status of a single cluster
func (s *Server) checkClusterStatus(ctx context.Context, clusterName string) ClusterStatus {
	status := ClusterStatus{
		Name:   clusterName,
		Status: "Unknown",
	}

	// Check if this is the active cluster
	clusters := s.clusterManager.ListClusters()
	for _, c := range clusters {
		if c.Name == clusterName && c.IsActive {
			status.IsActive = true
			break
		}
	}

	// Use WithFreshManagerForCluster to ensure proper cleanup
	err := s.clusterManager.WithFreshManagerForCluster(clusterName, func(manager *kubernetes.Manager) error {
		// Test API connectivity using discovery client
		discoveryClient, err := manager.ToDiscoveryClient()
		if err != nil {
			status.Status = "NotReady"
			status.LastError = fmt.Sprintf("Failed to get discovery client: %v", err)
			return nil // Don't return error, we've already captured it
		}

		start := time.Now()
		version, err := discoveryClient.ServerVersion()
		status.APILatency = time.Since(start)

		if err != nil {
			status.Status = "NotReady"
			status.LastError = err.Error()
			return nil // Don't return error, we've already captured it
		}

		status.Status = "Ready"
		status.Version = version.GitVersion
		return nil
	})

	if err != nil {
		status.Status = "NotReady"
		status.LastError = fmt.Sprintf("Failed to create manager: %v", err)
	}

	// For more detailed status, we would need to create a derived Kubernetes client
	// But for now, we'll keep it simple with just the discovery check
	// The detailed node/pod/namespace counts would require:
	// derived, err := manager.Derived(checkCtx)
	// and then using the derived client to query resources

	return status
}

// formatClusterStatusTable formats cluster statuses as a table
func (s *Server) formatClusterStatusTable(statuses []ClusterStatus) *mcp.CallToolResult {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	// Header
	fmt.Fprintln(w, "CLUSTER\tSTATUS\tVERSION\tNODES\tNAMESPACES\tPODS\tKUBECONFIG-AGE\tLATENCY\tERRORS")
	fmt.Fprintln(w, "-------\t------\t-------\t-----\t----------\t----\t--------------\t-------\t------")

	for _, s := range statuses {
		cluster := s.Name
		if s.IsActive {
			cluster = cluster + " *"
		}

		nodes := "-"
		if s.NodeCount > 0 {
			nodes = fmt.Sprintf("%d/%d", s.ReadyNodes, s.NodeCount)
		}

		namespaces := "-"
		if s.NamespaceCount > 0 {
			namespaces = fmt.Sprintf("%d", s.NamespaceCount)
		}

		pods := "-"
		if s.PodCount > 0 {
			pods = fmt.Sprintf("%d/%d", s.RunningPods, s.PodCount)
		}

		age := humanizeDuration(s.KubeconfigAge)
		latency := "-"
		if s.APILatency > 0 {
			latency = fmt.Sprintf("%dms", s.APILatency.Milliseconds())
		}

		errors := "-"
		if s.LastError != "" {
			// Truncate long error messages
			if len(s.LastError) > 40 {
				errors = s.LastError[:37] + "..."
			} else {
				errors = s.LastError
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			cluster, s.Status, s.Version, nodes, namespaces, pods, age, latency, errors)
	}

	w.Flush()
	buf.WriteString("\n* = current active cluster\n")

	return NewTextResult(buf.String(), nil)
}

// formatDetailedClusterStatus formats detailed status for a single cluster
func (s *Server) formatDetailedClusterStatus(status ClusterStatus) *mcp.CallToolResult {
	var result strings.Builder

	result.WriteString(fmt.Sprintf("Cluster: %s\n", status.Name))
	result.WriteString(fmt.Sprintf("Status: %s\n", status.Status))

	if status.Status == "Ready" {
		result.WriteString(fmt.Sprintf("Version: %s\n", status.Version))
		result.WriteString(fmt.Sprintf("Nodes: %d (Ready: %d)\n", status.NodeCount, status.ReadyNodes))
		result.WriteString(fmt.Sprintf("Namespaces: %d\n", status.NamespaceCount))
		result.WriteString(fmt.Sprintf("Pods: %d (Running: %d)\n", status.PodCount, status.RunningPods))
		result.WriteString(fmt.Sprintf("API Latency: %dms\n", status.APILatency.Milliseconds()))
	}

	if status.LastError != "" {
		result.WriteString(fmt.Sprintf("Error: %s\n", status.LastError))
	}

	result.WriteString(fmt.Sprintf("Kubeconfig Age: %s\n", humanizeDuration(status.KubeconfigAge)))

	if status.IsActive {
		result.WriteString("Status: ACTIVE\n")
	}

	return NewTextResult(result.String(), nil)
}

// clustersRefresh handles the clusters_refresh tool
func (s *Server) clustersRefresh(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.clusterManager == nil {
		return NewTextResult("", fmt.Errorf("cluster manager not initialized")), nil
	}

	var result strings.Builder

	// Refresh clusters
	if err := s.clusterManager.RefreshClusters(ctx); err != nil {
		return NewTextResult("", fmt.Errorf("failed to refresh clusters: %w", err)), nil
	}

	clusters := s.clusterManager.ListClusters()
	result.WriteString(fmt.Sprintf("Discovered %d clusters\n", len(clusters)))

	// List discovered clusters
	if len(clusters) > 0 {
		result.WriteString("\nClusters:\n")
		for _, cluster := range clusters {
			// For now, just list the clusters without health status
			// Health status would need to be checked via the health monitor
			result.WriteString(fmt.Sprintf("  - %s\n", cluster.Name))
		}
	}

	return NewTextResult(result.String(), nil), nil
}

// executeOperation dynamically executes an MCP operation with the given arguments
func (s *Server) executeOperation(ctx context.Context, operation string, args map[string]interface{}) (string, error) {
	// Create a CallToolRequest with the operation arguments
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      operation,
			Arguments: args,
		},
	}

	// Route to the appropriate handler based on operation name
	var result *mcp.CallToolResult
	var err error

	switch operation {
	// Resource operations
	case "resources_list":
		result, err = s.resourcesList(ctx, req)
	case "resources_get":
		result, err = s.resourcesGet(ctx, req)
	case "resources_create_or_update":
		result, err = s.resourcesCreateOrUpdate(ctx, req)
	case "resources_delete":
		result, err = s.resourcesDelete(ctx, req)

	// Pod operations
	case "pods_list":
		result, err = s.podsListInAllNamespaces(ctx, req)
	case "pods_list_in_namespace":
		result, err = s.podsListInNamespace(ctx, req)
	case "pods_get":
		result, err = s.podsGet(ctx, req)
	case "pods_log":
		result, err = s.podsLog(ctx, req)
	case "pods_exec":
		result, err = s.podsExec(ctx, req)
	case "pods_delete":
		result, err = s.podsDelete(ctx, req)
	case "pods_run":
		result, err = s.podsRun(ctx, req)
	case "pods_top":
		result, err = s.podsTop(ctx, req)

	// Namespace operations
	case "namespaces_list":
		result, err = s.namespacesList(ctx, req)

	// Event operations
	case "events_list":
		result, err = s.eventsList(ctx, req)

	// Helm operations
	case "helm_list":
		result, err = s.helmList(ctx, req)
	case "helm_install":
		result, err = s.helmInstall(ctx, req)
	case "helm_uninstall":
		result, err = s.helmUninstall(ctx, req)

	default:
		return "", fmt.Errorf("unsupported operation: %s", operation)
	}

	if err != nil {
		return "", err
	}

	// Extract text content from the result
	if result != nil && len(result.Content) > 0 {
		// Assuming the result contains text content
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			return textContent.Text, nil
		}
		// Handle other content types if needed
		return fmt.Sprintf("%v", result.Content[0]), nil
	}

	return "", nil
}

// humanizeDuration converts a duration to a human-readable string
func humanizeDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
