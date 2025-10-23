package jobs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
	"k8s.io/klog/v2"
)

// RancherDownloadExecutor executes Rancher cluster downloads asynchronously
type RancherDownloadExecutor struct {
	rancher    *kubernetes.RancherIntegration
	configDir  string
	clusterMgr interface{ RefreshClusters(context.Context) error } // Accept any cluster manager with RefreshClusters
	logger     klog.Logger
}

// NewRancherDownloadExecutor creates a new Rancher download executor
func NewRancherDownloadExecutor(rancher *kubernetes.RancherIntegration, configDir string, clusterMgr interface{ RefreshClusters(context.Context) error }) *RancherDownloadExecutor {
	return &RancherDownloadExecutor{
		rancher:    rancher,
		configDir:  configDir,
		clusterMgr: clusterMgr,
		logger:     klog.Background(),
	}
}

// Execute runs the Rancher download job
func (e *RancherDownloadExecutor) Execute(
	ctx context.Context,
	params map[string]interface{},
	resultsChan chan<- JobResult,
	progressChan chan<- JobProgress,
) error {
	// Get cluster list
	e.logger.Info("Listing clusters from Rancher")
	clusterNames, err := e.rancher.ListClusters(ctx)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	totalClusters := len(clusterNames)
	e.logger.Info("Starting Rancher download", "clusters", totalClusters)

	progressChan <- JobProgress{
		Total:   totalClusters,
		Message: fmt.Sprintf("Downloading %d clusters from Rancher", totalClusters),
	}

	// Download concurrently with controlled parallelism
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent downloads
	done := atomic.Int32{}

	for _, clusterName := range clusterNames {
		// Check for cancellation
		select {
		case <-ctx.Done():
			e.logger.Info("Rancher download cancelled", "completed", done.Load(), "total", totalClusters)
			return ctx.Err()
		case semaphore <- struct{}{}:
		}

		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			e.downloadCluster(ctx, name, resultsChan, progressChan, &done, totalClusters)
		}(clusterName)
	}

	// Wait for all downloads to complete
	wg.Wait()

	finalCount := int(done.Load())
	e.logger.Info("Rancher download completed", "completed", finalCount, "total", totalClusters)

	// Refresh cluster manager to pick up new kubeconfigs
	if e.clusterMgr != nil {
		e.logger.Info("Refreshing cluster manager")
		if err := e.clusterMgr.RefreshClusters(ctx); err != nil {
			e.logger.Error(err, "Failed to refresh cluster manager")
			return fmt.Errorf("downloaded clusters but failed to refresh cluster manager: %w", err)
		}
	}

	progressChan <- JobProgress{
		Done:       finalCount,
		Total:      totalClusters,
		Percentage: 100.0,
		Message:    fmt.Sprintf("Completed: %d/%d clusters", finalCount, totalClusters),
	}

	return nil
}

// downloadCluster downloads a single cluster's kubeconfig
func (e *RancherDownloadExecutor) downloadCluster(
	ctx context.Context,
	clusterName string,
	resultsChan chan<- JobResult,
	progressChan chan<- JobProgress,
	done *atomic.Int32,
	total int,
) {
	start := time.Now()
	result := JobResult{
		Cluster:  clusterName,
		Metadata: make(map[string]string),
	}

	e.logger.V(2).Info("Downloading cluster kubeconfig", "cluster", clusterName)

	// Download with 30s per-cluster timeout
	downloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := e.rancher.DownloadKubeconfig(downloadCtx, clusterName, e.configDir)
	duration := time.Since(start)
	result.DurationMs = duration.Milliseconds()

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		e.logger.Error(err, "Failed to download cluster kubeconfig", "cluster", clusterName, "duration", duration)
	} else {
		result.Success = true
		result.Result = map[string]interface{}{
			"downloaded": true,
			"saved":      true,
		}
		e.logger.V(2).Info("Downloaded cluster kubeconfig", "cluster", clusterName, "duration", duration)
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
		Message:    fmt.Sprintf("Downloaded %s (%d/%d)", clusterName, current, total),
	}
}
