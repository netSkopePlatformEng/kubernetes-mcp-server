package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
)

// SimpleMultiClusterManager manages multiple Kubernetes clusters with proper resource cleanup
type SimpleMultiClusterManager struct {
	staticConfig      *config.StaticConfig
	availableClusters map[string]string // cluster name -> kubeconfig path
	activeCluster     string
	activeManager     *SimplifiedManager // Using SimplifiedManager for better Rancher support
	logger            klog.Logger
	mutex             sync.RWMutex
}

// NewSimpleMultiClusterManager creates a new simplified multi-cluster manager
func NewSimpleMultiClusterManager(staticConfig *config.StaticConfig, logger klog.Logger) (*SimpleMultiClusterManager, error) {
	if !staticConfig.IsMultiClusterEnabled() {
		return nil, fmt.Errorf("multi-cluster mode not enabled")
	}

	mcm := &SimpleMultiClusterManager{
		staticConfig:      staticConfig,
		availableClusters: make(map[string]string),
		logger:            logger,
	}

	return mcm, nil
}

// Start initializes the multi-cluster manager and discovers clusters
func (mcm *SimpleMultiClusterManager) Start(ctx context.Context) error {
	mcm.logger.Info("Starting simplified multi-cluster manager")

	// Discover available clusters
	if err := mcm.discoverClusters(); err != nil {
		return fmt.Errorf("cluster discovery failed: %w", err)
	}

	// Set default cluster if specified
	if mcm.staticConfig.DefaultCluster != "" {
		if err := mcm.SwitchCluster(mcm.staticConfig.DefaultCluster); err != nil {
			mcm.logger.Error(err, "Failed to set default cluster", "cluster", mcm.staticConfig.DefaultCluster)
			// Don't fail, just log the error
		}
	} else if len(mcm.availableClusters) > 0 {
		// Auto-select first cluster if no default specified
		for name := range mcm.availableClusters {
			if err := mcm.SwitchCluster(name); err == nil {
				break
			}
		}
	}

	mcm.logger.Info("Simplified multi-cluster manager started",
		"clusters", len(mcm.availableClusters),
		"active", mcm.activeCluster)

	return nil
}

// Stop stops the multi-cluster manager and cleans up resources
func (mcm *SimpleMultiClusterManager) Stop() {
	mcm.logger.Info("Stopping simplified multi-cluster manager")

	mcm.mutex.Lock()
	defer mcm.mutex.Unlock()

	// Close the active manager if it exists
	if mcm.activeManager != nil {
		mcm.logger.V(2).Info("Closing active manager", "cluster", mcm.activeCluster)
		mcm.activeManager.Close()
		mcm.activeManager = nil
	}

	mcm.activeCluster = ""
	mcm.logger.Info("Simplified multi-cluster manager stopped")
}

// discoverClusters discovers available clusters from kubeconfig directory
func (mcm *SimpleMultiClusterManager) discoverClusters() error {
	mcm.mutex.Lock()
	defer mcm.mutex.Unlock()

	mcm.availableClusters = make(map[string]string)

	if mcm.staticConfig.KubeConfigDir == "" {
		return fmt.Errorf("no kubeconfig directory specified")
	}

	mcm.logger.V(2).Info("Scanning kubeconfig directory", "directory", mcm.staticConfig.KubeConfigDir)

	// Walk the directory and find kubeconfig files
	err := filepath.Walk(mcm.staticConfig.KubeConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Check if file is a YAML kubeconfig file
		if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
			clusterName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			mcm.availableClusters[clusterName] = path
			mcm.logger.V(2).Info("Discovered cluster", "name", clusterName, "path", path)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to scan kubeconfig directory: %w", err)
	}

	mcm.logger.Info("Cluster discovery completed", "count", len(mcm.availableClusters))
	return nil
}

// SwitchCluster switches to a different cluster with proper cleanup
func (mcm *SimpleMultiClusterManager) SwitchCluster(clusterName string) error {
	mcm.mutex.Lock()
	defer mcm.mutex.Unlock()

	// Check if cluster exists
	kubeconfigPath, exists := mcm.availableClusters[clusterName]
	if !exists {
		availableClusters := make([]string, 0, len(mcm.availableClusters))
		for name := range mcm.availableClusters {
			availableClusters = append(availableClusters, name)
		}
		return fmt.Errorf("cluster %s not found, available clusters: %v", clusterName, availableClusters)
	}

	// If we're already on this cluster, do nothing
	if mcm.activeCluster == clusterName && mcm.activeManager != nil {
		mcm.logger.V(2).Info("Already on cluster", "cluster", clusterName)
		return nil
	}

	previousCluster := mcm.activeCluster

	// Close the old manager if it exists
	if mcm.activeManager != nil {
		mcm.logger.V(2).Info("Closing previous manager", "cluster", previousCluster)
		mcm.activeManager.Close()
		mcm.activeManager = nil
	}

	mcm.logger.Info("Creating new simplified manager", "cluster", clusterName, "kubeconfig", kubeconfigPath)

	// Create a new simplified manager for this cluster
	// This uses direct config building which better preserves Rancher proxy paths
	newManager, err := NewSimplifiedManager(clusterName, kubeconfigPath, mcm.staticConfig, mcm.logger)
	if err != nil {
		return fmt.Errorf("failed to create simplified manager for cluster %s: %w", clusterName, err)
	}

	// Validate the new manager
	restConfig, err := newManager.ToRESTConfig()
	if err != nil || restConfig == nil {
		newManager.Close()
		return fmt.Errorf("cluster %s manager has invalid rest config: %w", clusterName, err)
	}

	// Set the new active cluster and manager
	mcm.activeCluster = clusterName
	mcm.activeManager = newManager

	mcm.logger.Info("Successfully switched cluster",
		"from", previousCluster,
		"to", clusterName,
		"kubeconfig", kubeconfigPath,
		"server", restConfig.Host)

	return nil
}

// GetActiveCluster returns the currently active cluster name
func (mcm *SimpleMultiClusterManager) GetActiveCluster() string {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()
	return mcm.activeCluster
}

// GetActiveManager returns the Manager for the currently active cluster
func (mcm *SimpleMultiClusterManager) GetActiveManager() (*Manager, error) {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()

	if mcm.activeCluster == "" {
		return nil, fmt.Errorf("no active cluster")
	}

	if mcm.activeManager == nil {
		return nil, fmt.Errorf("active cluster %s has no manager", mcm.activeCluster)
	}

	// Convert SimplifiedManager to Manager for compatibility
	return mcm.activeManager.ConvertToManager(), nil
}

// GetManager returns the Manager for a specific cluster (creates it if needed)
func (mcm *SimpleMultiClusterManager) GetManager(clusterName string) (*Manager, error) {
	mcm.mutex.Lock()
	defer mcm.mutex.Unlock()

	// If it's the active cluster, return the active manager
	if clusterName == mcm.activeCluster && mcm.activeManager != nil {
		return mcm.activeManager.ConvertToManager(), nil
	}

	// Otherwise, we need to create a temporary manager
	kubeconfigPath, exists := mcm.availableClusters[clusterName]
	if !exists {
		return nil, fmt.Errorf("cluster %s not found", clusterName)
	}

	// Create a temporary simplified manager
	// Note: Caller is responsible for closing this manager
	tempManager, err := NewSimplifiedManager(clusterName, kubeconfigPath, mcm.staticConfig, mcm.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary manager for cluster %s: %w", clusterName, err)
	}

	// Convert to Manager for compatibility
	return tempManager.ConvertToManager(), nil
}

// ListClusters returns information about all available clusters
func (mcm *SimpleMultiClusterManager) ListClusters() []ClusterConfig {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()

	clusters := make([]ClusterConfig, 0, len(mcm.availableClusters))

	for name, kubeconfig := range mcm.availableClusters {
		config := ClusterConfig{
			Name:       name,
			KubeConfig: kubeconfig,
			IsActive:   name == mcm.activeCluster,
		}
		clusters = append(clusters, config)
	}

	return clusters
}

// RefreshClusters refreshes the cluster list
func (mcm *SimpleMultiClusterManager) RefreshClusters(ctx context.Context) error {
	mcm.logger.V(2).Info("Refreshing cluster list")
	return mcm.discoverClusters()
}

// GetClusterCount returns the number of available clusters
func (mcm *SimpleMultiClusterManager) GetClusterCount() int {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()
	return len(mcm.availableClusters)
}
