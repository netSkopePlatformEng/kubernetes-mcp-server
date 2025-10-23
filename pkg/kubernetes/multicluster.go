package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
)

// MultiClusterManager extends the existing Manager to support multiple clusters
type MultiClusterManager struct {
	staticConfig  *config.StaticConfig
	clusters      map[string]*Manager
	activeCluster string
	healthMonitor *ClusterHealthMonitor
	logger        klog.Logger
	mutex         sync.RWMutex
}

// ClusterConfig represents the configuration for a single cluster
type ClusterConfig struct {
	Name         string    `json:"name"`
	KubeConfig   string    `json:"kubeconfig_path"`
	IsActive     bool      `json:"is_active"`
	LastAccessed time.Time `json:"last_accessed"`
	Environment  string    `json:"environment,omitempty"`
	Description  string    `json:"description,omitempty"`
}

// NewMultiClusterManager creates a new multi-cluster manager
func NewMultiClusterManager(staticConfig *config.StaticConfig, logger klog.Logger) (*MultiClusterManager, error) {
	if !staticConfig.IsMultiClusterEnabled() {
		return nil, fmt.Errorf("multi-cluster mode not enabled")
	}

	mcm := &MultiClusterManager{
		staticConfig: staticConfig,
		clusters:     make(map[string]*Manager),
		logger:       logger,
	}

	// Initialize health monitor
	healthConfig := DefaultClusterHealthConfig()
	mcm.healthMonitor = NewClusterHealthMonitor(healthConfig, logger, mcm.performHealthCheck)

	return mcm, nil
}

// Start initializes the multi-cluster manager and discovers clusters
func (mcm *MultiClusterManager) Start(ctx context.Context) error {
	mcm.logger.Info("Starting multi-cluster manager")

	// Discover and initialize clusters
	if err := mcm.DiscoverClusters(ctx); err != nil {
		return fmt.Errorf("cluster discovery failed: %w", err)
	}

	// Start health monitoring for discovered clusters
	clusterNames := make([]string, 0, len(mcm.clusters))
	for name := range mcm.clusters {
		clusterNames = append(clusterNames, name)
	}
	mcm.healthMonitor.Start(ctx, clusterNames)

	// Set default cluster if specified
	if mcm.staticConfig.DefaultCluster != "" {
		if err := mcm.SwitchCluster(mcm.staticConfig.DefaultCluster); err != nil {
			mcm.logger.Error(err, "Failed to set default cluster", "cluster", mcm.staticConfig.DefaultCluster)
		}
	} else if len(mcm.clusters) > 0 {
		// Auto-select first cluster if no default specified
		for name := range mcm.clusters {
			mcm.activeCluster = name
			break
		}
	}

	mcm.logger.Info("Multi-cluster manager started", "clusters", len(mcm.clusters), "active", mcm.activeCluster)
	return nil
}

// Stop stops the multi-cluster manager
func (mcm *MultiClusterManager) Stop() {
	mcm.logger.Info("Stopping multi-cluster manager")

	if mcm.healthMonitor != nil {
		mcm.healthMonitor.Stop()
	}

	// Clean up all cluster managers
	mcm.mutex.Lock()
	for name, manager := range mcm.clusters {
		mcm.logger.V(2).Info("Closing cluster manager", "cluster", name)
		manager.Close()
	}
	mcm.clusters = make(map[string]*Manager)
	mcm.mutex.Unlock()

	mcm.logger.Info("Multi-cluster manager stopped")
}

// DiscoverClusters discovers available clusters from kubeconfig directory
func (mcm *MultiClusterManager) DiscoverClusters(ctx context.Context) error {
	mcm.logger.V(2).Info("Discovering clusters")

	// Scan kubeconfig directory
	discoveredClusters, err := mcm.scanKubeConfigDirectory()
	if err != nil {
		return fmt.Errorf("failed to scan kubeconfig directory: %w", err)
	}

	// Apply cluster aliases
	discoveredClusters = mcm.applyClusterAliases(discoveredClusters)

	// Initialize Manager instances for each cluster
	newClusters := make(map[string]*Manager)
	var failedClusters []string
	for name, kubeconfigPath := range discoveredClusters {
		mcm.logger.V(2).Info("Creating manager for cluster", "cluster", name, "kubeconfig", kubeconfigPath)
		manager, err := mcm.createClusterManager(name, kubeconfigPath)
		if err != nil {
			mcm.logger.Error(err, "Failed to create manager for cluster", "cluster", name, "kubeconfig", kubeconfigPath)
			failedClusters = append(failedClusters, name)
			continue
		}
		newClusters[name] = manager
		mcm.logger.V(2).Info("Successfully created manager for cluster", "cluster", name)
	}

	if len(failedClusters) > 0 {
		mcm.logger.Info("Some clusters failed to initialize", "failed_clusters", failedClusters, "successful_clusters", len(newClusters), "total_discovered", len(discoveredClusters))
	}

	mcm.mutex.Lock()
	oldClusters := mcm.clusters
	mcm.clusters = newClusters
	mcm.mutex.Unlock()

	// Clean up removed clusters and update health monitoring
	if mcm.healthMonitor != nil {
		// Remove clusters that are no longer available
		for oldCluster, oldManager := range oldClusters {
			if _, exists := newClusters[oldCluster]; !exists {
				mcm.logger.V(2).Info("Cleaning up removed cluster", "cluster", oldCluster)
				mcm.healthMonitor.RemoveCluster(oldCluster)
				oldManager.Close()
			}
		}

		// Add new clusters to monitoring
		for newCluster := range newClusters {
			if _, exists := oldClusters[newCluster]; !exists {
				mcm.healthMonitor.AddCluster(newCluster)
			}
		}
	} else {
		// Just clean up removed clusters if no health monitoring
		for oldCluster, oldManager := range oldClusters {
			if _, exists := newClusters[oldCluster]; !exists {
				mcm.logger.V(2).Info("Cleaning up removed cluster", "cluster", oldCluster)
				oldManager.Close()
			}
		}
	}

	mcm.logger.V(2).Info("Cluster discovery completed", "count", len(newClusters))
	return nil
}

// scanKubeConfigDirectory scans the kubeconfig directory for cluster files
func (mcm *MultiClusterManager) scanKubeConfigDirectory() (map[string]string, error) {
	clusters := make(map[string]string)

	if mcm.staticConfig.KubeConfigDir == "" {
		mcm.logger.V(2).Info("No kubeconfig directory specified")
		return clusters, nil
	}

	mcm.logger.V(2).Info("Scanning kubeconfig directory", "directory", mcm.staticConfig.KubeConfigDir)

	err := filepath.Walk(mcm.staticConfig.KubeConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			mcm.logger.Error(err, "Error walking directory", "path", path)
			return err
		}

		if info.IsDir() {
			mcm.logger.V(3).Info("Skipping directory", "path", path)
			return nil
		}

		mcm.logger.V(3).Info("Found file", "path", path, "name", info.Name())

		// Check if file is a YAML kubeconfig file
		if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
			// Extract cluster name from filename
			basename := filepath.Base(path)
			clusterName := strings.TrimSuffix(basename, filepath.Ext(basename))
			clusters[clusterName] = path
			mcm.logger.V(2).Info("Discovered cluster config", "cluster", clusterName, "path", path)

			// Ensure kubeconfig file has secure permissions (0600 = rw-------)
			if err := os.Chmod(path, 0600); err != nil {
				mcm.logger.Error(err, "Failed to set secure permissions on kubeconfig", "path", path)
				// Don't fail discovery, just log the error
			}
		} else {
			mcm.logger.V(3).Info("Skipping non-YAML file", "path", path)
		}

		return nil
	})

	mcm.logger.V(2).Info("Directory scan completed", "clusters_found", len(clusters))
	for name, path := range clusters {
		mcm.logger.V(2).Info("Cluster discovered", "name", name, "path", path)
	}

	return clusters, err
}

// applyClusterAliases applies cluster aliases from configuration
func (mcm *MultiClusterManager) applyClusterAliases(clusters map[string]string) map[string]string {
	if len(mcm.staticConfig.ClusterAliases) == 0 {
		return clusters
	}

	aliased := make(map[string]string)

	// First, add all original clusters
	for name, path := range clusters {
		aliased[name] = path
	}

	// Then add aliases
	for alias, target := range mcm.staticConfig.ClusterAliases {
		if path, exists := clusters[target]; exists {
			aliased[alias] = path
		}
	}

	return aliased
}

// createClusterManager creates a Manager instance for a specific cluster
func (mcm *MultiClusterManager) createClusterManager(clusterName, kubeconfigPath string) (*Manager, error) {
	mcm.logger.V(2).Info("Creating manager for cluster", "cluster", clusterName, "kubeconfig", kubeconfigPath)

	// Create a completely new static config for each cluster to ensure isolation
	clusterConfig := &config.StaticConfig{
		KubeConfig:         kubeconfigPath,
		KubeConfigDir:      "", // Clear multi-cluster config to use single kubeconfig
		ReadOnly:           mcm.staticConfig.ReadOnly,
		DisableDestructive: mcm.staticConfig.DisableDestructive,
		EnabledTools:       mcm.staticConfig.EnabledTools,
		DisabledTools:      mcm.staticConfig.DisabledTools,
		RequireOAuth:       mcm.staticConfig.RequireOAuth,
		DefaultCluster:     mcm.staticConfig.DefaultCluster,
		ClusterAliases:     mcm.staticConfig.ClusterAliases,
		RancherIntegration: mcm.staticConfig.RancherIntegration,
	}

	mcm.logger.V(3).Info("Creating manager with config", "cluster", clusterName,
		"kubeconfig", clusterConfig.KubeConfig)

	// Force fresh client creation by clearing any cached configurations
	// This ensures we don't inherit stale authentication state

	mcm.logger.V(3).Info("Creating manager with config", "cluster", clusterName,
		"kubeconfig", clusterConfig.KubeConfig)

	// Create manager for this cluster
	manager, err := NewManager(clusterConfig)
	if err != nil {
		mcm.logger.Error(err, "Failed to create manager", "cluster", clusterName, "kubeconfig", kubeconfigPath)
		return nil, fmt.Errorf("failed to create manager for cluster %s: %w", clusterName, err)
	}

	// Validate the manager has proper configuration
	restConfig, err := manager.ToRESTConfig()
	if err != nil || restConfig == nil {
		return nil, fmt.Errorf("cluster %s manager has invalid rest config: %w", clusterName, err)
	}

	// Validate authentication is configured
	authMethod := "unknown"
	if restConfig.BearerToken != "" || restConfig.BearerTokenFile != "" {
		authMethod = "bearer-token"
		mcm.logger.V(3).Info("Cluster has bearer token authentication",
			"cluster", clusterName,
			"tokenLength", len(restConfig.BearerToken),
			"tokenFile", restConfig.BearerTokenFile)
	} else if restConfig.Username != "" && restConfig.Password != "" {
		authMethod = "basic-auth"
	} else if (restConfig.CertFile != "" && restConfig.KeyFile != "") ||
		(len(restConfig.CertData) > 0 && len(restConfig.KeyData) > 0) {
		authMethod = "client-cert"
	} else if restConfig.ExecProvider != nil {
		authMethod = "exec-provider"
	} else if restConfig.AuthProvider != nil {
		authMethod = "auth-provider"
	}

	if authMethod == "unknown" {
		mcm.logger.V(1).Info("WARNING: Cluster manager has no authentication configured",
			"cluster", clusterName,
			"server", restConfig.Host)
		// Don't fail here, as the cluster might still work with anonymous access
		// or the authentication might be handled differently
	}

	mcm.logger.V(2).Info("Successfully created manager for cluster",
		"cluster", clusterName,
		"server", restConfig.Host,
		"authMethod", authMethod)

	return manager, nil
}

// SwitchCluster switches to a different cluster
func (mcm *MultiClusterManager) SwitchCluster(clusterName string) error {
	mcm.mutex.Lock()
	defer mcm.mutex.Unlock()

	// Check if cluster exists
	manager, exists := mcm.clusters[clusterName]
	if !exists {
		availableClusters := make([]string, 0, len(mcm.clusters))
		for name := range mcm.clusters {
			availableClusters = append(availableClusters, name)
		}
		return fmt.Errorf("cluster %s not found, available clusters: %v", clusterName, availableClusters)
	}

	// Validate the manager is properly configured
	if manager == nil {
		return fmt.Errorf("cluster %s has no manager instance", clusterName)
	}
	if manager.staticConfig == nil {
		return fmt.Errorf("cluster %s manager has no configuration", clusterName)
	}
	if manager.staticConfig.KubeConfig == "" {
		return fmt.Errorf("cluster %s manager has no kubeconfig path", clusterName)
	}

	// Store previous cluster for logging
	previousCluster := mcm.activeCluster

	mcm.logger.V(2).Info("Attempting cluster switch",
		"from", previousCluster,
		"to", clusterName,
		"kubeconfig", manager.staticConfig.KubeConfig,
		"server", func() string {
			if restConfig, err := manager.ToRESTConfig(); err == nil && restConfig != nil {
				return restConfig.Host
			}
			return "unknown"
		}())

	// Validate cluster connectivity before switching
	if err := mcm.validateClusterConnection(manager, clusterName); err != nil {
		mcm.logger.Error(err, "Cluster validation failed", "cluster", clusterName)
		return fmt.Errorf("cluster %s is not reachable: %w", clusterName, err)
	}
	if manager.staticConfig.KubeConfig == "" {
		return fmt.Errorf("cluster %s manager has no kubeconfig path", clusterName)
	}

	// Store previous cluster for logging
	previousCluster := mcm.activeCluster

	// Switch to the new cluster - each manager is pre-configured with its own kubeconfig
	mcm.activeCluster = clusterName

	// Log detailed information about the new active cluster's authentication
	if restConfig, err := manager.ToRESTConfig(); err == nil && restConfig != nil {
		authInfo := "unknown"
		if restConfig.BearerToken != "" {
			authInfo = fmt.Sprintf("bearer-token (%d chars)", len(restConfig.BearerToken))
		} else if restConfig.BearerTokenFile != "" {
			authInfo = fmt.Sprintf("bearer-token-file (%s)", restConfig.BearerTokenFile)
		} else if restConfig.Username != "" {
			authInfo = "basic-auth"
		} else if restConfig.CertFile != "" || len(restConfig.CertData) > 0 {
			authInfo = "client-cert"
		}

		mcm.logger.Info("Switched cluster",
			"from", previousCluster,
			"to", clusterName,
			"kubeconfig", manager.staticConfig.KubeConfig,
			"server", restConfig.Host,
			"auth", authInfo)
	} else {
		mcm.logger.Info("Switched cluster",
			"from", previousCluster,
			"to", clusterName,
			"kubeconfig", manager.staticConfig.KubeConfig)
	}

	return nil
}

// GetActiveCluster returns the currently active cluster
func (mcm *MultiClusterManager) GetActiveCluster() string {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()
	return mcm.activeCluster
}

// GetActiveManager returns the Manager for the currently active cluster
func (mcm *MultiClusterManager) GetActiveManager() (*Manager, error) {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()

	if mcm.activeCluster == "" {
		return nil, fmt.Errorf("no active cluster")
	}

	manager, exists := mcm.clusters[mcm.activeCluster]
	if !exists {
		return nil, fmt.Errorf("active cluster %s not found", mcm.activeCluster)
	}

	return manager, nil
}

// GetManager returns the Manager for a specific cluster
func (mcm *MultiClusterManager) GetManager(clusterName string) (*Manager, error) {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()

	manager, exists := mcm.clusters[clusterName]
	if !exists {
		return nil, fmt.Errorf("cluster %s not found", clusterName)
	}

	return manager, nil
}

// ListClusters returns information about all available clusters
func (mcm *MultiClusterManager) ListClusters() []ClusterConfig {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()

	clusters := make([]ClusterConfig, 0, len(mcm.clusters))

	for name, manager := range mcm.clusters {
		config := ClusterConfig{
			Name:       name,
			KubeConfig: manager.staticConfig.KubeConfig,
			IsActive:   name == mcm.activeCluster,
		}

		clusters = append(clusters, config)
	}

	return clusters
}

// ValidateCluster checks if a cluster is accessible
func (mcm *MultiClusterManager) ValidateCluster(clusterName string) error {
	manager, err := mcm.GetManager(clusterName)
	if err != nil {
		return err
	}

	// Basic validation - check if config is valid
	if manager.cfg == nil {
		return fmt.Errorf("cluster %s has invalid configuration", clusterName)
	}

	return nil
}

// validateClusterConnection performs a quick connectivity check to the cluster API server
func (mcm *MultiClusterManager) validateClusterConnection(manager *Manager, clusterName string) error {
	// Check if manager is nil
	if manager == nil {
		return fmt.Errorf("manager is nil for cluster %s", clusterName)
	}

	// Get discovery client to test API connectivity
	discoveryClient, err := manager.ToDiscoveryClient()
	if err != nil {
		return fmt.Errorf("failed to create discovery client: %w", err)
	}

	// Check if discovery client is nil (can happen if manager not fully initialized)
	if discoveryClient == nil {
		// Manager not fully initialized - skip API connectivity test
		mcm.logger.V(2).Info("Skipping cluster validation - discovery client not initialized", "cluster", clusterName)
		return nil
	}

	// Perform a lightweight API call to verify connectivity with timeout
	// ServerVersion is a simple GET request that doesn't require special permissions
	// The timeout is handled by the underlying HTTP client configuration
	_, err = discoveryClient.ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to connect to cluster API: %w", err)
	}

	mcm.logger.V(2).Info("Cluster connection validated", "cluster", clusterName)
	return nil
}

// RefreshClusters refreshes the cluster list
func (mcm *MultiClusterManager) RefreshClusters(ctx context.Context) error {
	mcm.logger.V(2).Info("Refreshing clusters")

	// Rediscover clusters
	return mcm.DiscoverClusters(ctx)
}

// GetClusterCount returns the number of available clusters
func (mcm *MultiClusterManager) GetClusterCount() int {
	mcm.mutex.RLock()
	defer mcm.mutex.RUnlock()
	return len(mcm.clusters)
}

// performHealthCheck performs a health check on a specific cluster
func (mcm *MultiClusterManager) performHealthCheck(ctx context.Context, cluster string) error {
	mcm.logger.V(3).Info("Performing health check", "cluster", cluster)

	manager, err := mcm.GetManager(cluster)
	if err != nil {
		mcm.logger.V(3).Info("Failed to get manager for health check", "cluster", cluster, "error", err)
		return fmt.Errorf("failed to get manager for cluster %s: %w", cluster, err)
	}

	// Validate manager configuration
	restConfig, err := manager.ToRESTConfig()
	if err != nil || restConfig == nil {
		mcm.logger.V(3).Info("Manager has no rest config", "cluster", cluster, "error", err)
		return fmt.Errorf("cluster %s manager has no rest config: %w", cluster, err)
	}

	// Try to get the discovery client and perform a simple API call
	client, err := manager.ToDiscoveryClient()
	if err != nil {
		mcm.logger.V(3).Info("Failed to get discovery client", "cluster", cluster, "error", err)
		return fmt.Errorf("failed to get discovery client for cluster %s: %w", cluster, err)
	}

	// Perform a simple API call to check cluster health and authentication
	serverVersion, err := client.ServerVersion()
	if err != nil {
		mcm.logger.V(3).Info("Health check failed", "cluster", cluster, "error", err, "server", restConfig.Host)

		// Provide more detailed error messages for common authentication issues
		if strings.Contains(err.Error(), "credentials") || strings.Contains(err.Error(), "Unauthorized") {
			return fmt.Errorf("cluster %s authentication failed - server requested credentials: %w", cluster, err)
		}
		return fmt.Errorf("cluster %s health check failed: %w", cluster, err)
	}

	mcm.logger.V(3).Info("Health check successful", "cluster", cluster,
		"server", restConfig.Host, "version", serverVersion.String())

	return nil
}

// IsClusterHealthy returns true if the cluster is healthy
func (mcm *MultiClusterManager) IsClusterHealthy(cluster string) bool {
	if mcm.healthMonitor == nil {
		return true // Assume healthy if monitoring is disabled
	}
	return mcm.healthMonitor.IsHealthy(cluster)
}

// GetClusterHealth returns the health status of a specific cluster
func (mcm *MultiClusterManager) GetClusterHealth(cluster string) (*ClusterHealthStatus, bool) {
	if mcm.healthMonitor == nil {
		return nil, false
	}
	return mcm.healthMonitor.GetClusterStatus(cluster)
}

// GetAllClusterHealth returns the health status of all clusters
func (mcm *MultiClusterManager) GetAllClusterHealth() map[string]ClusterHealthStatus {
	if mcm.healthMonitor == nil {
		return make(map[string]ClusterHealthStatus)
	}
	return mcm.healthMonitor.GetAllStatuses()
}

// GetHealthySummary returns a summary of healthy vs total clusters
func (mcm *MultiClusterManager) GetHealthySummary() (healthy, total int) {
	if mcm.healthMonitor == nil {
		return len(mcm.clusters), len(mcm.clusters)
	}
	return mcm.healthMonitor.GetHealthySummary()
}
