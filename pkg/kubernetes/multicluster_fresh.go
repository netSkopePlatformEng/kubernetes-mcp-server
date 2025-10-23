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

// FreshMultiClusterManager manages multiple Kubernetes clusters with fresh clients for each operation.
// This approach mirrors kubectl's behavior: create fresh clients for each command to avoid state persistence issues.
type FreshMultiClusterManager struct {
	staticConfig      *config.StaticConfig
	availableClusters map[string]string // cluster name -> kubeconfig path
	activeCluster     string
	logger            klog.Logger
	mutex             sync.RWMutex
}

// NewFreshMultiClusterManager creates a new fresh multi-cluster manager that creates clients on-demand
func NewFreshMultiClusterManager(staticConfig *config.StaticConfig, logger klog.Logger) (*FreshMultiClusterManager, error) {
	if !staticConfig.IsMultiClusterEnabled() {
		return nil, fmt.Errorf("multi-cluster mode not enabled")
	}

	mcm := &FreshMultiClusterManager{
		staticConfig:      staticConfig,
		availableClusters: make(map[string]string),
		logger:            logger,
	}

	return mcm, nil
}

// Start initializes the multi-cluster manager and discovers clusters
func (mcm *FreshMultiClusterManager) Start(ctx context.Context) error {
	mcm.logger.Info("Starting fresh multi-cluster manager (kubectl-style)")

	// Discover available clusters
	if err := mcm.discoverClusters(); err != nil {
		return fmt.Errorf("cluster discovery failed: %w", err)
	}

	// Set default cluster if specified
	if mcm.staticConfig.DefaultCluster != "" {
		if _, exists := mcm.availableClusters[mcm.staticConfig.DefaultCluster]; exists {
			mcm.activeCluster = mcm.staticConfig.DefaultCluster
			mcm.logger.Info("Set default cluster", "cluster", mcm.activeCluster)
		} else {
			mcm.logger.Error(nil, "Default cluster not found", "cluster", mcm.staticConfig.DefaultCluster)
		}
	} else if len(mcm.availableClusters) > 0 {
		// Auto-select first cluster if no default specified
		for name := range mcm.availableClusters {
			mcm.activeCluster = name
			break
		}
	}

	mcm.logger.Info("Fresh multi-cluster manager started",
		"clusters", len(mcm.availableClusters),
		"active", mcm.activeCluster)

	return nil
}

// Stop stops the multi-cluster manager (no persistent resources to clean up)
func (mcm *FreshMultiClusterManager) Stop() {
	mcm.logger.Info("Stopping fresh multi-cluster manager")
	// No persistent managers to clean up - they're created fresh per operation
	mcm.logger.Info("Fresh multi-cluster manager stopped")
}

// discoverClusters discovers available clusters from kubeconfig directory
func (mcm *FreshMultiClusterManager) discoverClusters() error {
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

// SwitchCluster switches the active cluster (just changes the name, no manager caching)
func (mcm *FreshMultiClusterManager) SwitchCluster(clusterName string) error {
	mcm.mutex.Lock()
	defer mcm.mutex.Unlock()

	// Check if cluster exists
	_, exists := mcm.availableClusters[clusterName]
	if !exists {
		availableClusters := make([]string, 0, len(mcm.availableClusters))
		for name := range mcm.availableClusters {
			availableClusters = append(availableClusters, name)
		}
		return fmt.Errorf("cluster %s not found, available clusters: %v", clusterName, availableClusters)
	}

	previousCluster := mcm.activeCluster
	mcm.activeCluster = clusterName

	mcm.logger.Info("Switched active cluster",
		"from", previousCluster,
		"to", clusterName)

	return nil
}

// GetActiveCluster returns the currently active cluster name
func (mcm *FreshMultiClusterManager) GetActiveCluster() string {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()
	return mcm.activeCluster
}

// GetActiveManager creates a fresh Manager for the currently active cluster
// IMPORTANT: Caller MUST close the returned manager when done to avoid resource leaks
func (mcm *FreshMultiClusterManager) GetActiveManager() (*Manager, error) {
	mcm.mutex.RLock()
	clusterName := mcm.activeCluster
	kubeconfigPath := mcm.availableClusters[clusterName]
	mcm.mutex.RUnlock()

	if clusterName == "" {
		return nil, fmt.Errorf("no active cluster")
	}

	if kubeconfigPath == "" {
		return nil, fmt.Errorf("no kubeconfig path for cluster %s", clusterName)
	}

	mcm.logger.V(2).Info("Creating fresh manager for active cluster", "cluster", clusterName)

	// Create a fresh simplified manager
	freshManager, err := NewSimplifiedManager(clusterName, kubeconfigPath, mcm.staticConfig, mcm.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create fresh manager for cluster %s: %w", clusterName, err)
	}

	// Convert to Manager for compatibility
	return freshManager.ConvertToManager(), nil
}

// GetManager creates a fresh Manager for a specific cluster
// IMPORTANT: Caller MUST close the returned manager when done to avoid resource leaks
func (mcm *FreshMultiClusterManager) GetManager(clusterName string) (*Manager, error) {
	mcm.mutex.RLock()
	kubeconfigPath := mcm.availableClusters[clusterName]
	mcm.mutex.RUnlock()

	if kubeconfigPath == "" {
		return nil, fmt.Errorf("cluster %s not found", clusterName)
	}

	mcm.logger.V(2).Info("Creating fresh manager for cluster", "cluster", clusterName)

	// Create a fresh simplified manager
	freshManager, err := NewSimplifiedManager(clusterName, kubeconfigPath, mcm.staticConfig, mcm.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create fresh manager for cluster %s: %w", clusterName, err)
	}

	// Convert to Manager for compatibility
	return freshManager.ConvertToManager(), nil
}

// ListClusters returns information about all available clusters
func (mcm *FreshMultiClusterManager) ListClusters() []ClusterConfig {
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
func (mcm *FreshMultiClusterManager) RefreshClusters(ctx context.Context) error {
	mcm.logger.V(2).Info("Refreshing cluster list")
	return mcm.discoverClusters()
}

// GetClusterCount returns the number of available clusters
func (mcm *FreshMultiClusterManager) GetClusterCount() int {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()
	return len(mcm.availableClusters)
}

// WithFreshManager executes a function with a fresh manager for the active cluster.
// This is the recommended way to use the FreshMultiClusterManager - it ensures proper cleanup.
func (mcm *FreshMultiClusterManager) WithFreshManager(fn func(*Manager) error) error {
	manager, err := mcm.GetActiveManager()
	if err != nil {
		return err
	}
	defer manager.Close() // Always clean up

	return fn(manager)
}

// WithFreshManagerForCluster executes a function with a fresh manager for a specific cluster.
// This ensures proper cleanup of the manager after use.
func (mcm *FreshMultiClusterManager) WithFreshManagerForCluster(clusterName string, fn func(*Manager) error) error {
	manager, err := mcm.GetManager(clusterName)
	if err != nil {
		return err
	}
	defer manager.Close() // Always clean up

	return fn(manager)
}
