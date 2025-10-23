package rancher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
)

// Manager handles Rancher API integration for downloading and managing kubeconfig files
type Manager struct {
	client *Client
	config *config.RancherConfig
	logger klog.Logger

	// State management
	mu            sync.RWMutex
	lastRefresh   time.Time
	clusterStates map[string]string // cluster name -> state (active, error, etc.)
	downloadCount int

	// Auto-refresh control
	refreshTicker *time.Ticker
	stopRefresh   chan struct{}
}

// NewManager creates a new Rancher manager
func NewManager(cfg *config.RancherConfig, logger klog.Logger) *Manager {
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	client := NewClient(cfg.URL, cfg.Token, cfg.Insecure)

	manager := &Manager{
		client:        client,
		config:        cfg,
		logger:        logger,
		clusterStates: make(map[string]string),
		stopRefresh:   make(chan struct{}),
	}

	// Start auto-refresh if configured
	if cfg.AutoRefresh && cfg.RefreshInterval != "" {
		duration, err := time.ParseDuration(cfg.RefreshInterval)
		if err != nil {
			logger.Error(err, "Invalid refresh interval, using default",
				"interval", cfg.RefreshInterval)
			duration = time.Hour
		}
		manager.startAutoRefresh(duration)
	}

	return manager
}

// startAutoRefresh starts a background goroutine to periodically refresh clusters
func (m *Manager) startAutoRefresh(interval time.Duration) {
	m.refreshTicker = time.NewTicker(interval)

	go func() {
		for {
			select {
			case <-m.refreshTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				if err := m.DownloadAll(ctx); err != nil {
					m.logger.Error(err, "Auto-refresh failed")
				}
				cancel()
			case <-m.stopRefresh:
				m.refreshTicker.Stop()
				return
			}
		}
	}()
}

// Stop stops the manager and any background operations
func (m *Manager) Stop() {
	if m.refreshTicker != nil {
		close(m.stopRefresh)
		m.refreshTicker.Stop()
	}
}

// ListClusters returns a list of cluster names from Rancher
func (m *Manager) ListClusters(ctx context.Context) ([]string, error) {
	clusters, err := m.client.ListClusters()
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	// Apply filtering if configured
	filtered := m.filterClusters(clusters)

	// Extract names and update states
	m.mu.Lock()
	names := make([]string, 0, len(filtered))
	for _, cluster := range filtered {
		names = append(names, cluster.Name)
		m.clusterStates[cluster.Name] = cluster.State
	}
	m.mu.Unlock()

	return names, nil
}

// filterClusters applies configured filters to the cluster list
func (m *Manager) filterClusters(clusters []Cluster) []Cluster {
	var filtered []Cluster

	// Compile pattern regex if configured
	var pattern *regexp.Regexp
	if m.config.ClusterPattern != "" {
		var err error
		pattern, err = regexp.Compile(m.config.ClusterPattern)
		if err != nil {
			m.logger.Error(err, "Invalid cluster pattern", "pattern", m.config.ClusterPattern)
		}
	}

	for _, cluster := range clusters {
		// Check pattern match
		if pattern != nil && !pattern.MatchString(cluster.Name) {
			continue
		}

		// Check exclude list
		excluded := false
		for _, exclude := range m.config.ExcludeClusters {
			if cluster.Name == exclude {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		// Check include list (if specified, only include those)
		if len(m.config.IncludeClusters) > 0 {
			included := false
			for _, include := range m.config.IncludeClusters {
				if cluster.Name == include {
					included = true
					break
				}
			}
			if !included {
				continue
			}
		}

		filtered = append(filtered, cluster)
	}

	return filtered
}

// DownloadKubeconfig downloads and saves a kubeconfig for a specific cluster
func (m *Manager) DownloadKubeconfig(ctx context.Context, clusterName string, outputDir string) error {
	// Get the cluster
	cluster, err := m.client.GetCluster(clusterName)
	if err != nil {
		return fmt.Errorf("failed to get cluster %s: %w", clusterName, err)
	}

	// Generate kubeconfig
	kubeconfig, err := m.client.GenerateKubeconfig(cluster)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig for %s: %w", clusterName, err)
	}

	// Ensure output directory exists
	if outputDir == "" {
		outputDir = m.config.ConfigDir
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Save to file
	filename := filepath.Join(outputDir, fmt.Sprintf("%s.yaml", clusterName))
	if err := os.WriteFile(filename, []byte(kubeconfig), 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	m.logger.Info("Downloaded kubeconfig",
		"cluster", clusterName,
		"file", filename)

	// Update state
	m.mu.Lock()
	m.clusterStates[clusterName] = cluster.State
	m.downloadCount++
	m.mu.Unlock()

	return nil
}

// DownloadAll downloads kubeconfigs for all clusters
func (m *Manager) DownloadAll(ctx context.Context) error {
	clusters, err := m.client.ListClusters()
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	// Apply filtering
	filtered := m.filterClusters(clusters)

	m.logger.Info("Downloading kubeconfigs",
		"total", len(clusters),
		"filtered", len(filtered))

	// Track results
	var errors []string
	successCount := 0

	for _, cluster := range filtered {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := m.DownloadKubeconfig(ctx, cluster.Name, m.config.ConfigDir); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", cluster.Name, err))
			m.logger.Error(err, "Failed to download kubeconfig", "cluster", cluster.Name)
		} else {
			successCount++
		}
	}

	// Update last refresh time
	m.mu.Lock()
	m.lastRefresh = time.Now()
	m.mu.Unlock()

	if len(errors) > 0 {
		return fmt.Errorf("downloaded %d/%d clusters, errors: %s",
			successCount, len(filtered), strings.Join(errors, "; "))
	}

	return nil
}

// GetClustersWithStatus returns clusters with their current status
func (m *Manager) GetClustersWithStatus(ctx context.Context) ([]map[string]string, error) {
	clusters, err := m.client.ListClusters()
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	// Apply filtering
	filtered := m.filterClusters(clusters)

	result := make([]map[string]string, 0, len(filtered))
	for _, cluster := range filtered {
		result = append(result, map[string]string{
			"name":  cluster.Name,
			"id":    cluster.ID,
			"state": cluster.State,
		})
	}

	return result, nil
}

// GetStatus returns the current status of the Rancher integration
func (m *Manager) GetStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := map[string]interface{}{
		"enabled":       m.config.Enabled,
		"rancher_url":   m.config.URL,
		"config_dir":    m.config.ConfigDir,
		"last_refresh":  m.lastRefresh.Format(time.RFC3339),
		"cluster_count": len(m.clusterStates),
		"auto_refresh":  m.config.AutoRefresh,
	}

	if m.config.AutoRefresh {
		status["refresh_interval"] = m.config.RefreshInterval
	}

	return status
}

// RefreshCluster refreshes the kubeconfig for a specific cluster
func (m *Manager) RefreshCluster(ctx context.Context, clusterName string) error {
	return m.DownloadKubeconfig(ctx, clusterName, m.config.ConfigDir)
}
