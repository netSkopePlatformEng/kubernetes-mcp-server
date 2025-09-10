package nsk

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/containers/kubernetes-mcp-server/pkg/config"
)

// Manager provides high-level NSK integration functionality
type Manager struct {
	client        *NSKClient
	config        *config.NSKConfig
	logger        klog.Logger
	refreshTicker *time.Ticker
	stopChan      chan struct{}
	mutex         sync.RWMutex
	running       bool
}

// NewManager creates a new NSK manager
func NewManager(config *config.NSKConfig, logger klog.Logger) (*Manager, error) {
	client, err := NewNSKClient(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create NSK client: %w", err)
	}

	return &Manager{
		client:   client,
		config:   config,
		logger:   logger,
		stopChan: make(chan struct{}),
	}, nil
}

// Start begins the NSK manager operations
func (m *Manager) Start(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.running {
		return fmt.Errorf("NSK manager is already running")
	}

	m.logger.Info("Starting NSK manager")

	// Initial cluster discovery and kubeconfig download
	if err := m.client.DiscoverClusters(ctx); err != nil {
		return fmt.Errorf("initial cluster discovery failed: %w", err)
	}

	if err := m.client.DownloadKubeConfigs(ctx); err != nil {
		return fmt.Errorf("initial kubeconfig download failed: %w", err)
	}

	// Start auto-refresh if enabled
	if m.config.AutoRefresh {
		refreshInterval, err := time.ParseDuration(m.config.RefreshInterval)
		if err != nil {
			return fmt.Errorf("invalid refresh interval: %w", err)
		}

		m.refreshTicker = time.NewTicker(refreshInterval)
		go m.refreshLoop(ctx)
	}

	m.running = true
	m.logger.Info("NSK manager started successfully")
	return nil
}

// Stop stops the NSK manager operations
func (m *Manager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.running {
		return
	}

	m.logger.Info("Stopping NSK manager")

	close(m.stopChan)
	if m.refreshTicker != nil {
		m.refreshTicker.Stop()
	}

	m.running = false
	m.logger.Info("NSK manager stopped")
}

// refreshLoop handles periodic refresh of cluster information
func (m *Manager) refreshLoop(ctx context.Context) {
	for {
		select {
		case <-m.refreshTicker.C:
			m.logger.V(2).Info("Performing scheduled cluster refresh")
			if err := m.RefreshClusters(ctx); err != nil {
				m.logger.Error(err, "Scheduled cluster refresh failed")
			}
		case <-m.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
}

// RefreshClusters manually refreshes cluster information
func (m *Manager) RefreshClusters(ctx context.Context) error {
	m.logger.V(2).Info("Refreshing cluster information")

	if err := m.client.DiscoverClusters(ctx); err != nil {
		return fmt.Errorf("cluster discovery failed: %w", err)
	}

	if err := m.client.DownloadKubeConfigs(ctx); err != nil {
		return fmt.Errorf("kubeconfig download failed: %w", err)
	}

	m.logger.V(2).Info("Cluster refresh completed")
	return nil
}

// GetClusters returns all discovered clusters
func (m *Manager) GetClusters() map[string]*ClusterInfo {
	return m.client.GetClusters()
}

// GetCluster returns information about a specific cluster
func (m *Manager) GetCluster(name string) (*ClusterInfo, error) {
	return m.client.GetCluster(name)
}

// GetKubeConfigPath returns the path to a cluster's kubeconfig file
func (m *Manager) GetKubeConfigPath(clusterName string) (string, error) {
	cluster, err := m.client.GetCluster(clusterName)
	if err != nil {
		return "", err
	}

	if cluster.KubeConfig == "" {
		return "", fmt.Errorf("kubeconfig path not available for cluster %s", clusterName)
	}

	return cluster.KubeConfig, nil
}

// ListClusterNames returns a slice of all cluster names
func (m *Manager) ListClusterNames() []string {
	clusters := m.client.GetClusters()
	names := make([]string, 0, len(clusters))
	for name := range clusters {
		names = append(names, name)
	}
	return names
}

// GetClustersByEnvironment returns clusters filtered by environment
func (m *Manager) GetClustersByEnvironment(environment string) map[string]*ClusterInfo {
	allClusters := m.client.GetClusters()
	filtered := make(map[string]*ClusterInfo)

	for name, cluster := range allClusters {
		if cluster.Environment == environment {
			filtered[name] = cluster
		}
	}

	return filtered
}

// ValidateCluster checks if a cluster exists and is accessible
func (m *Manager) ValidateCluster(clusterName string) error {
	cluster, err := m.client.GetCluster(clusterName)
	if err != nil {
		return err
	}

	// Check if kubeconfig file exists
	if cluster.KubeConfig != "" {
		if !fileExists(cluster.KubeConfig) {
			return fmt.Errorf("kubeconfig file not found: %s", cluster.KubeConfig)
		}
	}

	return nil
}

// IsHealthy performs a health check of the NSK integration
func (m *Manager) IsHealthy(ctx context.Context) error {
	return m.client.IsHealthy(ctx)
}

// GetStatus returns the current status of the NSK manager
func (m *Manager) GetStatus() *ManagerStatus {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	clusters := m.client.GetClusters()
	status := &ManagerStatus{
		Running:      m.running,
		ClusterCount: len(clusters),
		AutoRefresh:  m.config.AutoRefresh,
		LastRefresh:  time.Time{},
	}

	// Find the most recent refresh time
	for _, cluster := range clusters {
		if cluster.LastRefresh.After(status.LastRefresh) {
			status.LastRefresh = cluster.LastRefresh
		}
	}

	if m.config.AutoRefresh {
		status.RefreshInterval = m.config.RefreshInterval
	}

	return status
}

// ManagerStatus represents the current status of the NSK manager
type ManagerStatus struct {
	Running         bool      `json:"running"`
	ClusterCount    int       `json:"cluster_count"`
	AutoRefresh     bool      `json:"auto_refresh"`
	RefreshInterval string    `json:"refresh_interval,omitempty"`
	LastRefresh     time.Time `json:"last_refresh"`
}

// fileExists checks if a file exists
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}