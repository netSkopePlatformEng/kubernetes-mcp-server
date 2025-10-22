package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/rancher"
	"k8s.io/klog/v2"
)

// RancherIntegration manages Rancher integration for multi-cluster kubeconfig management
type RancherIntegration struct {
	config         *config.RancherConfig
	client         *rancher.Client
	clusterManager *MultiClusterManager
	logger         klog.Logger
	mu             sync.RWMutex
	lastRefresh    time.Time
}

// NewRancherIntegration creates a new Rancher integration
func NewRancherIntegration(cfg *config.RancherConfig, mcm *MultiClusterManager) *RancherIntegration {
	client := rancher.NewClient(cfg.URL, cfg.Token, cfg.Insecure)

	return &RancherIntegration{
		config:         cfg,
		client:         client,
		clusterManager: mcm,
		logger:         klog.Background(),
	}
}

// ListClusters lists all available clusters from Rancher
func (r *RancherIntegration) ListClusters(ctx context.Context) ([]string, error) {
	clusters, err := r.client.ListClusters()
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters from Rancher: %w", err)
	}

	names := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		names = append(names, cluster.Name)
	}

	return names, nil
}

// DownloadKubeconfig downloads kubeconfig for a specific cluster
func (r *RancherIntegration) DownloadKubeconfig(ctx context.Context, clusterName string, configDir string) error {
	r.logger.Info("Downloading kubeconfig", "cluster", clusterName)

	// Generate kubeconfig from Rancher
	kubeconfig, err := r.client.GenerateKubeconfigByName(clusterName)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig for cluster %s: %w", clusterName, err)
	}

	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write to file
	filename := filepath.Join(configDir, fmt.Sprintf("%s.yaml", clusterName))
	if err := os.WriteFile(filename, []byte(kubeconfig), 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig file: %w", err)
	}

	r.logger.Info("Kubeconfig downloaded", "cluster", clusterName, "file", filename)
	return nil
}

// DownloadAllKubeconfigs downloads kubeconfigs for all clusters
func (r *RancherIntegration) DownloadAllKubeconfigs(ctx context.Context, configDir string) ([]string, []string, error) {
	r.logger.Info("Downloading all kubeconfigs from Rancher")

	// Get all clusters
	clusterNames, err := r.ListClusters(ctx)
	if err != nil {
		return nil, nil, err
	}

	r.logger.Info("Found clusters", "count", len(clusterNames))

	var successful []string
	var failed []string
	var mu sync.Mutex

	// Download concurrently
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent downloads

	for _, clusterName := range clusterNames {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			if err := r.DownloadKubeconfig(ctx, name, configDir); err != nil {
				r.logger.Error(err, "Failed to download kubeconfig", "cluster", name)
				mu.Lock()
				failed = append(failed, name)
				mu.Unlock()
			} else {
				mu.Lock()
				successful = append(successful, name)
				mu.Unlock()
			}
		}(clusterName)
	}

	wg.Wait()

	r.mu.Lock()
	r.lastRefresh = time.Now()
	r.mu.Unlock()

	r.logger.Info("Download complete", "successful", len(successful), "failed", len(failed))

	return successful, failed, nil
}

// RefreshClusters triggers the cluster manager to reload kubeconfigs from disk
func (r *RancherIntegration) RefreshClusters(ctx context.Context) error {
	if r.clusterManager == nil {
		return fmt.Errorf("cluster manager not initialized")
	}
	return r.clusterManager.RefreshClusters(ctx)
}

// GetLastRefreshTime returns the last time kubeconfigs were downloaded
func (r *RancherIntegration) GetLastRefreshTime() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastRefresh
}

// GetStatus returns the current Rancher integration status
func (r *RancherIntegration) GetStatus() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return map[string]interface{}{
		"enabled":       true,
		"rancher_url":   r.config.URL,
		"config_dir":    r.config.ConfigDir,
		"last_refresh":  r.lastRefresh,
		"cluster_count": len(r.clusterManager.ListClusters()),
	}
}

// GetClustersWithStatus returns all clusters with their current status from Rancher
func (r *RancherIntegration) GetClustersWithStatus(ctx context.Context) ([]map[string]string, error) {
	clusters, err := r.client.ListClusters()
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters from Rancher: %w", err)
	}

	result := make([]map[string]string, 0, len(clusters))
	for _, cluster := range clusters {
		result = append(result, map[string]string{
			"id":    cluster.ID,
			"name":  cluster.Name,
			"state": cluster.State,
		})
	}

	return result, nil
}
