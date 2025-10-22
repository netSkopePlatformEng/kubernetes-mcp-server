package jobs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"
)

// ClusterExecExecutor executes operations across multiple clusters
type ClusterExecExecutor struct {
	executeFunc func(ctx context.Context, cluster string, operation string, args map[string]interface{}) (string, error)
	logger      klog.Logger
}

// NewClusterExecExecutor creates a new cluster exec executor
func NewClusterExecExecutor(executeFunc func(ctx context.Context, cluster string, operation string, args map[string]interface{}) (string, error)) *ClusterExecExecutor {
	return &ClusterExecExecutor{
		executeFunc: executeFunc,
		logger:      klog.Background(),
	}
}

// Execute runs an operation across multiple clusters
func (e *ClusterExecExecutor) Execute(
	ctx context.Context,
	params map[string]interface{},
	resultsChan chan<- JobResult,
	progressChan chan<- JobProgress,
) error {
	// Extract parameters
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return fmt.Errorf("operation is required")
	}

	clusters, ok := params["clusters"].([]string)
	if !ok || len(clusters) == 0 {
		// This should have been validated before job creation
		return fmt.Errorf("clusters list is required")
	}

	args, _ := params["arguments"].(map[string]interface{})
	if args == nil {
		args = make(map[string]interface{})
	}

	fanoutLimit := 10
	if fl, ok := params["fanout_limit"].(int); ok {
		fanoutLimit = fl
	} else if fl, ok := params["fanout_limit"].(float64); ok {
		fanoutLimit = int(fl)
	}

	timeoutPerCluster := 30 * time.Second
	if tpc, ok := params["timeout_per_cluster"].(string); ok {
		if d, err := time.ParseDuration(tpc); err == nil {
			timeoutPerCluster = d
		}
	} else if tpc, ok := params["timeout_per_cluster"].(time.Duration); ok {
		timeoutPerCluster = tpc
	}

	continueOnError := true
	if coe, ok := params["continue_on_error"].(bool); ok {
		continueOnError = coe
	}

	total := len(clusters)
	e.logger.Info("Starting cluster exec", "operation", operation, "clusters", total, "fanout", fanoutLimit)

	progressChan <- JobProgress{
		Total:   total,
		Message: fmt.Sprintf("Executing '%s' on %d clusters", operation, total),
	}

	// Execute on clusters with bounded concurrency
	sem := make(chan struct{}, fanoutLimit)
	var wg sync.WaitGroup
	done := atomic.Int32{}
	shouldStop := atomic.Bool{}

	for _, cluster := range clusters {
		// Check if we should stop (only if continueOnError is false)
		if !continueOnError && shouldStop.Load() {
			e.logger.Info("Stopping execution due to error", "cluster", cluster)
			break
		}

		// Check for cancellation
		select {
		case <-ctx.Done():
			e.logger.Info("Cluster exec cancelled", "completed", done.Load(), "total", total)
			return ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(cluster string) {
			defer wg.Done()
			defer func() { <-sem }()

			success := e.executeOnCluster(ctx, cluster, operation, args, timeoutPerCluster, resultsChan, progressChan, &done, total)

			// If execution failed and continueOnError is false, signal to stop
			if !success && !continueOnError {
				shouldStop.Store(true)
			}
		}(cluster)
	}

	// Wait for all executions to complete
	wg.Wait()

	finalCount := int(done.Load())
	e.logger.Info("Cluster exec completed", "operation", operation, "completed", finalCount, "total", total)

	progressChan <- JobProgress{
		Done:       finalCount,
		Total:      total,
		Percentage: 100.0,
		Message:    fmt.Sprintf("Completed: %d/%d clusters", finalCount, total),
	}

	return nil
}

// executeOnCluster executes an operation on a single cluster
func (e *ClusterExecExecutor) executeOnCluster(
	ctx context.Context,
	cluster string,
	operation string,
	args map[string]interface{},
	timeout time.Duration,
	resultsChan chan<- JobResult,
	progressChan chan<- JobProgress,
	done *atomic.Int32,
	total int,
) bool {
	start := time.Now()
	result := JobResult{
		Cluster:  cluster,
		Metadata: make(map[string]string),
	}

	e.logger.V(2).Info("Executing operation on cluster", "cluster", cluster, "operation", operation)

	// Per-cluster timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, err := e.executeFunc(execCtx, cluster, operation, args)
	duration := time.Since(start)
	result.DurationMs = duration.Milliseconds()

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		e.logger.Error(err, "Failed to execute operation on cluster", "cluster", cluster, "operation", operation, "duration", duration)
	} else {
		result.Success = true
		result.Result = map[string]interface{}{
			"output": output,
		}
		e.logger.V(2).Info("Executed operation on cluster", "cluster", cluster, "operation", operation, "duration", duration)
	}

	// Send result
	resultsChan <- result

	// Update progress
	current := done.Add(1)
	percentage := float64(current) / float64(total) * 100

	progressChan <- JobProgress{
		Done:       int(current),
		Total:      total,
		Percentage: percentage,
		Message:    fmt.Sprintf("Processed %s (%d/%d)", cluster, current, total),
	}

	return result.Success
}
