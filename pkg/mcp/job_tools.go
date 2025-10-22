package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/mcp/jobs"
)

// initJobTools registers all job-related MCP tools
func (s *Server) initJobTools() []server.ServerTool {
	// Only return job tools if JobManager is initialized
	if s.jobManager == nil {
		return []server.ServerTool{}
	}

	return []server.ServerTool{
		// Cluster Exec Jobs
		{Tool: mcp.NewTool("start_clusters_exec",
			mcp.WithDescription("Start executing operation across multiple clusters (async). Returns job_id immediately."),
			mcp.WithString("operation", mcp.Description("Operation to execute (e.g., 'resources_list', 'pods_list')"), mcp.Required()),
			mcp.WithObject("arguments", mcp.Description("Arguments for the operation")),
			mcp.WithArray("clusters", mcp.Description("List of cluster names (empty = all clusters)")),
			mcp.WithNumber("fanout_limit", mcp.Description("Max concurrent executions (default: 10)")),
			mcp.WithString("timeout_per_cluster", mcp.Description("Timeout per cluster (e.g., '30s', '1m', default: '30s')")),
			mcp.WithBoolean("continue_on_error", mcp.Description("Continue if a cluster fails (default: true)")),
			mcp.WithString("idempotency_key", mcp.Description("Optional key to prevent duplicate jobs")),
			mcp.WithTitleAnnotation("Clusters: Start Exec (Async)"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.startClustersExec},

		// Universal job status/results/cancel tools
		{Tool: mcp.NewTool("get_job_status",
			mcp.WithDescription("Get the current status and progress of a background job"),
			mcp.WithString("job_id", mcp.Description("Job ID returned from start_* command"), mcp.Required()),
			mcp.WithTitleAnnotation("Jobs: Get Status"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.getJobStatus},

		{Tool: mcp.NewTool("get_job_results",
			mcp.WithDescription("Get paginated results from a completed or running job"),
			mcp.WithString("job_id", mcp.Description("Job ID"), mcp.Required()),
			mcp.WithString("cursor", mcp.Description("Pagination cursor from previous response (optional)")),
			mcp.WithNumber("page_size", mcp.Description("Number of results per page (default: 10, max: 50)")),
			mcp.WithBoolean("include_full_results", mcp.Description("Include full result details (default: false, only shows summary)")),
			mcp.WithTitleAnnotation("Jobs: Get Results"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.getJobResults},

		{Tool: mcp.NewTool("cancel_job",
			mcp.WithDescription("Cancel a running background job"),
			mcp.WithString("job_id", mcp.Description("Job ID to cancel"), mcp.Required()),
			mcp.WithTitleAnnotation("Jobs: Cancel"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		), Handler: s.cancelJob},
	}
}

// startClustersExec starts a cluster exec job
func (s *Server) startClustersExec(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	operation, ok := args["operation"].(string)
	if !ok || operation == "" {
		return NewTextResult("", fmt.Errorf("operation is required")), nil
	}

	operationArgs, _ := args["arguments"].(map[string]interface{})
	if operationArgs == nil {
		operationArgs = make(map[string]interface{})
	}

	idempotencyKey, _ := args["idempotency_key"].(string)

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
		k8s, err := s.getKubernetesWithMultiCluster()
		if err != nil {
			return NewTextResult("", fmt.Errorf("failed to get Kubernetes client: %w", err)), nil
		}

		clusters := k8s.ListClusters()
		for _, cluster := range clusters {
			targetClusters = append(targetClusters, cluster.Name)
		}
	}

	if len(targetClusters) == 0 {
		return NewTextResult("", fmt.Errorf("no clusters available")), nil
	}

	fanoutLimit := 10
	if fl, ok := args["fanout_limit"].(float64); ok {
		fanoutLimit = int(fl)
	}

	timeoutPerCluster := "30s"
	if tpc, ok := args["timeout_per_cluster"].(string); ok {
		timeoutPerCluster = tpc
	}

	continueOnError := true
	if coe, ok := args["continue_on_error"].(bool); ok {
		continueOnError = coe
	}

	// Create job parameters
	params := map[string]interface{}{
		"operation":           operation,
		"arguments":           operationArgs,
		"clusters":            targetClusters,
		"fanout_limit":        fanoutLimit,
		"timeout_per_cluster": timeoutPerCluster,
		"continue_on_error":   continueOnError,
	}

	// Create or get existing job
	job, created := s.jobManager.CreateOrGet(idempotencyKey, jobs.JobTypeClusterExec, params)

	// Start job if newly created
	if created {
		// Create executor with callback to our executeOnCluster method
		executor := jobs.NewClusterExecExecutor(s.executeOnCluster)
		if err := s.jobManager.StartJob(job.ID, executor); err != nil {
			return NewTextResult("", fmt.Errorf("failed to start job: %w", err)), nil
		}
	}

	message := "created"
	if !created {
		message = "already exists (idempotent)"
	}

	result := fmt.Sprintf(`Cluster Exec Job Started

Job ID: %s
Operation: %s
Target Clusters: %d
Status: %s
Started: %s

Use 'get_job_status' with this job_id to check progress.
Use 'get_job_results' to fetch results when complete.
`, job.ID, operation, len(targetClusters), message, job.StartedAt.Format(time.RFC3339))

	return NewTextResult(result, nil), nil
}

// getJobStatus retrieves job status and progress
func (s *Server) getJobStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobID := req.GetArguments()["job_id"].(string)

	job := s.jobManager.GetJob(jobID)
	if job == nil {
		return NewTextResult("", fmt.Errorf("job not found: %s", jobID)), nil
	}

	progress := job.GetProgress()
	state := job.GetState()
	elapsed := time.Since(job.StartedAt)

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Job Status\n\n"))
	result.WriteString(fmt.Sprintf("Job ID: %s\n", job.ID))
	result.WriteString(fmt.Sprintf("Type: %s\n", job.Type))
	result.WriteString(fmt.Sprintf("State: %s\n", state))
	result.WriteString(fmt.Sprintf("Progress: %d/%d (%.1f%%)\n", progress.Done, progress.Total, progress.Percentage))
	result.WriteString(fmt.Sprintf("Elapsed: %s\n", elapsed.Round(time.Second)))

	if progress.ETASeconds > 0 {
		result.WriteString(fmt.Sprintf("ETA: %ds\n", progress.ETASeconds))
	}

	if progress.Message != "" {
		result.WriteString(fmt.Sprintf("Current: %s\n", progress.Message))
	}

	if job.CompletedAt != nil {
		result.WriteString(fmt.Sprintf("Completed: %s\n", job.CompletedAt.Format(time.RFC3339)))
	}

	// Add instructions based on state
	if state == jobs.JobStateRunning || state == jobs.JobStateQueued {
		result.WriteString("\nJob is still running. Poll again or use get_job_results to fetch partial results.\n")
	} else {
		result.WriteString("\nJob is complete. Use get_job_results to fetch all results.\n")
	}

	return NewTextResult(result.String(), nil), nil
}

// getJobResults retrieves paginated job results
func (s *Server) getJobResults(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	jobID := args["job_id"].(string)
	cursor, _ := args["cursor"].(string)
	includeFullResults, _ := args["include_full_results"].(bool)

	// Default to 10, max 50
	pageSize := 10
	if ps, ok := args["page_size"].(float64); ok {
		pageSize = int(ps)
		if pageSize > 50 {
			pageSize = 50
		}
	}

	page, err := s.jobManager.Storage.GetResults(jobID, cursor, pageSize)
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get results: %w", err)), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Job Results (Page %d of %d)\n\n", page.PageNum, (page.TotalItems+pageSize-1)/pageSize))
	result.WriteString(fmt.Sprintf("Job ID: %s\n", page.JobID))
	result.WriteString(fmt.Sprintf("State: %s\n\n", page.State))

	// Display results
	for _, item := range page.Items {
		status := "✅"
		if !item.Success {
			status = "❌"
		}
		result.WriteString(fmt.Sprintf("%s %s - %dms\n", status, item.Cluster, item.DurationMs))
		if item.Error != "" {
			result.WriteString(fmt.Sprintf("   Error: %s\n", item.Error))
		}
		if item.Result != nil {
			if includeFullResults {
				result.WriteString(fmt.Sprintf("   Result: %v\n", item.Result))
			} else {
				// Show intelligent summary based on result type
				summary := s.summarizeResult(item.Result)
				result.WriteString(fmt.Sprintf("   Summary: %s\n", summary))
			}
		}
	}

	// Pagination info
	result.WriteString(fmt.Sprintf("\nShowing %d of %d results\n", len(page.Items), page.TotalItems))
	if page.NextCursor != "" {
		result.WriteString(fmt.Sprintf("More results available. Use cursor: %s\n", page.NextCursor))
	}

	// Summary
	result.WriteString(fmt.Sprintf("\nSummary:\n"))
	result.WriteString(fmt.Sprintf("- Total: %d\n", page.Summary.Total))
	result.WriteString(fmt.Sprintf("- Succeeded: %d\n", page.Summary.Succeeded))
	result.WriteString(fmt.Sprintf("- Failed: %d\n", page.Summary.Failed))
	if page.Summary.TotalDurationMs > 0 {
		result.WriteString(fmt.Sprintf("- Total Duration: %dms (avg: %dms)\n",
			page.Summary.TotalDurationMs,
			page.Summary.TotalDurationMs/int64(page.Summary.Total)))
	}

	return NewTextResult(result.String(), nil), nil
}

// summarizeResult creates an intelligent summary of a result object
func (s *Server) summarizeResult(result interface{}) string {
	if result == nil {
		return "No data"
	}

	// Convert to string and parse for key information
	resultStr := fmt.Sprintf("%v", result)

	// Count lines (helpful for table output)
	lines := strings.Split(strings.TrimSpace(resultStr), "\n")
	lineCount := len(lines)

	// Try to detect table format (NAME, NAMESPACE, STATUS, etc.)
	if lineCount > 1 && (strings.Contains(lines[0], "NAME") || strings.Contains(lines[0], "NAMESPACE")) {
		// This looks like kubectl table output
		// Count actual data rows (excluding header and separators)
		dataRows := 0
		for i, line := range lines {
			if i == 0 {
				continue // Skip header
			}
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "---") {
				dataRows++
			}
		}
		if dataRows > 0 {
			return fmt.Sprintf("%d resources (table format)", dataRows)
		}
	}

	// Try to extract key metrics based on common patterns
	// Look for "Items:" to count resources
	if strings.Contains(resultStr, "Items:") {
		// Count number of items (e.g., nodes, pods)
		itemCount := 0
		for _, line := range lines {
			if strings.Contains(line, "Name:") || strings.Contains(line, "- name:") {
				itemCount++
			}
		}
		if itemCount > 0 {
			return fmt.Sprintf("%d items found", itemCount)
		}
	}

	// Look for common success/status messages
	if strings.Contains(resultStr, "successfully") || strings.Contains(resultStr, "SUCCESS") {
		if len(resultStr) <= 200 {
			return resultStr
		}
		return fmt.Sprintf("%s... (success)", resultStr[:150])
	}

	// For shorter results, just show them entirely
	if len(resultStr) <= 200 {
		return resultStr
	}

	// For longer results, show first line + count
	if lineCount > 5 {
		return fmt.Sprintf("%s... (%d lines total, use include_full_results=true for complete output)", lines[0], lineCount)
	}

	// Fallback: show first 200 chars with ellipsis
	return fmt.Sprintf("%s... (use include_full_results=true for complete output)", resultStr[:200])
}

// cancelJob cancels a running job
func (s *Server) cancelJob(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobID := req.GetArguments()["job_id"].(string)

	if err := s.jobManager.CancelJob(jobID); err != nil {
		return NewTextResult("", err), nil
	}

	result := fmt.Sprintf("Job cancelled: %s\n\nPartial results may be available via get_job_results.\n", jobID)
	return NewTextResult(result, nil), nil
}

// executeOnCluster is a helper that switches to a cluster and executes an operation
func (s *Server) executeOnCluster(ctx context.Context, cluster string, operation string, args map[string]interface{}) (string, error) {
	k8s, err := s.getKubernetesWithMultiCluster()
	if err != nil {
		return "", fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	// Switch to the target cluster
	if err := k8s.SwitchCluster(cluster); err != nil {
		return "", fmt.Errorf("failed to switch to cluster: %w", err)
	}

	// Reload Kubernetes client for this cluster
	if err := s.reloadKubernetesClient(); err != nil {
		return "", fmt.Errorf("failed to reload client: %w", err)
	}

	// Execute the operation
	output, err := s.executeOperation(ctx, operation, args)
	if err != nil {
		return "", err
	}

	return output, nil
}
