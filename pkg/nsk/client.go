package nsk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/containers/kubernetes-mcp-server/pkg/config"
)

// NSKClient provides integration with the NSK (Netskope Kubernetes) tool
type NSKClient struct {
	config      *config.NSKConfig
	nskPath     string
	logger      klog.Logger
	lastRefresh time.Time
	clusters    map[string]*ClusterInfo
	mutex       sync.RWMutex

	// Resource cleanup
	ctx          context.Context
	cancel       context.CancelFunc
	cleanupFuncs []func() error
}

// ClusterInfo represents information about a discovered cluster
type ClusterInfo struct {
	Name        string    `json:"name"`
	KubeConfig  string    `json:"kubeconfig_path"`
	LastRefresh time.Time `json:"last_refresh"`
	Status      string    `json:"status"`
	Environment string    `json:"environment,omitempty"`
	Description string    `json:"description,omitempty"`
}

// NSKClusterListResponse represents the JSON response from `nsk cluster list --output json`
type NSKClusterListResponse struct {
	Clusters []NSKCluster `json:"clusters"`
}

// NSKCluster represents a cluster entry from NSK
type NSKCluster struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	State       string `json:"state"`
	Description string `json:"description"`
}

// sanitizeClusterName sanitizes cluster names to prevent command injection
// Only allows alphanumeric characters, dashes, underscores, and dots
func sanitizeClusterName(name string) string {
	if name == "" {
		return ""
	}

	// Allow only safe characters: alphanumeric, dash, underscore, dot
	reg := regexp.MustCompile("[^a-zA-Z0-9_.-]")
	sanitized := reg.ReplaceAllString(name, "")

	// Ensure the name is not empty after sanitization
	if sanitized == "" {
		return "invalid-cluster-name"
	}

	// Limit length to prevent excessive resource usage
	if len(sanitized) > 128 {
		sanitized = sanitized[:128]
	}

	return sanitized
}

// NewNSKClient creates a new NSK client with the given configuration
func NewNSKClient(config *config.NSKConfig, logger klog.Logger) (*NSKClient, error) {
	if config == nil {
		return nil, fmt.Errorf("NSK configuration is required")
	}

	nskPath := config.NSKPath
	if nskPath == "" {
		nskPath = "nsk"
	}

	// Verify NSK binary is available
	if _, err := exec.LookPath(nskPath); err != nil {
		return nil, fmt.Errorf("NSK binary not found in PATH: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := &NSKClient{
		config:       config,
		nskPath:      nskPath,
		logger:       logger,
		clusters:     make(map[string]*ClusterInfo),
		ctx:          ctx,
		cancel:       cancel,
		cleanupFuncs: make([]func() error, 0),
	}

	// Ensure config directory exists
	if config.ConfigDir != "" {
		if err := os.MkdirAll(config.ConfigDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", config.ConfigDir, err)
		}
	}

	return client, nil
}

// DiscoverClusters discovers available clusters using NSK
func (c *NSKClient) DiscoverClusters(ctx context.Context) error {
	c.logger.V(2).Info("Discovering clusters via NSK")

	// Use the more restrictive context
	useCtx := ctx
	if c.ctx.Err() != nil {
		return fmt.Errorf("NSK client is closed")
	}
	if ctx.Err() != nil {
		useCtx = c.ctx
	}

	// Execute nsk cluster list --output json
	cmd := exec.CommandContext(useCtx, c.nskPath, "cluster", "list", "--output", "json")

	// Set environment variables
	env := c.buildEnvironment()
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	// Parse JSON response
	var response NSKClusterListResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("failed to parse cluster list response: %w", err)
	}

	// Filter and process clusters
	discoveredClusters := make(map[string]*ClusterInfo)
	for _, cluster := range response.Clusters {
		if c.shouldIncludeCluster(cluster.Name) {
			clusterInfo := &ClusterInfo{
				Name:        cluster.Name,
				Status:      cluster.State,
				Environment: c.detectEnvironment(cluster.Name),
				Description: cluster.Description,
				LastRefresh: time.Now(),
			}

			// Set kubeconfig path
			if c.config.ConfigDir != "" {
				clusterInfo.KubeConfig = filepath.Join(c.config.ConfigDir, fmt.Sprintf("%s.yaml", cluster.Name))
			}

			discoveredClusters[cluster.Name] = clusterInfo
		}
	}

	// Update clusters map
	c.mutex.Lock()
	c.clusters = discoveredClusters
	c.lastRefresh = time.Now()
	c.mutex.Unlock()

	c.logger.V(2).Info("Cluster discovery completed", "count", len(discoveredClusters))
	return nil
}

// DownloadKubeConfigs downloads kubeconfig files for all discovered clusters
func (c *NSKClient) DownloadKubeConfigs(ctx context.Context) error {
	c.mutex.RLock()
	clusters := make(map[string]*ClusterInfo)
	for k, v := range c.clusters {
		clusters[k] = v
	}
	c.mutex.RUnlock()

	if len(clusters) == 0 {
		return fmt.Errorf("no clusters discovered, run DiscoverClusters first")
	}

	c.logger.V(2).Info("Downloading kubeconfig files", "count", len(clusters))

	// Download kubeconfigs for each cluster
	for name, clusterInfo := range clusters {
		if err := c.downloadClusterKubeConfig(ctx, name, clusterInfo.KubeConfig); err != nil {
			c.logger.Error(err, "Failed to download kubeconfig", "cluster", name)
			// Continue with other clusters even if one fails
			continue
		}

		c.logger.V(3).Info("Downloaded kubeconfig", "cluster", name, "path", clusterInfo.KubeConfig)
	}

	return nil
}

// downloadClusterKubeConfig downloads kubeconfig for a specific cluster
func (c *NSKClient) downloadClusterKubeConfig(ctx context.Context, clusterName, outputPath string) error {
	// Sanitize cluster name to prevent command injection
	sanitizedClusterName := sanitizeClusterName(clusterName)
	if sanitizedClusterName != clusterName {
		c.logger.V(1).Info("Cluster name was sanitized", "original", clusterName, "sanitized", sanitizedClusterName)
	}

	// Validate output path to prevent path traversal
	if !filepath.IsAbs(outputPath) || strings.Contains(outputPath, "..") {
		return fmt.Errorf("invalid output path: %s", outputPath)
	}

	// Use the more restrictive context
	useCtx := ctx
	if c.ctx.Err() != nil {
		return fmt.Errorf("NSK client is closed")
	}
	if ctx.Err() != nil {
		useCtx = c.ctx
	}

	// Execute nsk cluster kubeconfig --name=<cluster> --output=<path>
	cmd := exec.CommandContext(useCtx, c.nskPath, "cluster", "kubeconfig",
		fmt.Sprintf("--name=%s", sanitizedClusterName),
		fmt.Sprintf("--output=%s", outputPath))

	// Set environment variables
	env := c.buildEnvironment()
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download kubeconfig for cluster %s: %w", clusterName, err)
	}

	return nil
}

// GetClusters returns a copy of the discovered clusters
func (c *NSKClient) GetClusters() map[string]*ClusterInfo {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	clusters := make(map[string]*ClusterInfo)
	for k, v := range c.clusters {
		// Create a copy
		clusters[k] = &ClusterInfo{
			Name:        v.Name,
			KubeConfig:  v.KubeConfig,
			LastRefresh: v.LastRefresh,
			Status:      v.Status,
			Environment: v.Environment,
			Description: v.Description,
		}
	}
	return clusters
}

// GetCluster returns information about a specific cluster
func (c *NSKClient) GetCluster(name string) (*ClusterInfo, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	cluster, exists := c.clusters[name]
	if !exists {
		return nil, fmt.Errorf("cluster %s not found", name)
	}

	// Return a copy
	return &ClusterInfo{
		Name:        cluster.Name,
		KubeConfig:  cluster.KubeConfig,
		LastRefresh: cluster.LastRefresh,
		Status:      cluster.Status,
		Environment: cluster.Environment,
		Description: cluster.Description,
	}, nil
}

// RefreshIfNeeded refreshes cluster information if auto-refresh is enabled and interval has passed
func (c *NSKClient) RefreshIfNeeded(ctx context.Context) error {
	if !c.config.AutoRefresh {
		return nil
	}

	refreshInterval, err := time.ParseDuration(c.config.RefreshInterval)
	if err != nil {
		c.logger.Error(err, "Invalid refresh interval", "interval", c.config.RefreshInterval)
		return err
	}

	c.mutex.RLock()
	needsRefresh := time.Since(c.lastRefresh) > refreshInterval
	c.mutex.RUnlock()

	if needsRefresh {
		c.logger.V(2).Info("Auto-refreshing cluster information")
		if err := c.DiscoverClusters(ctx); err != nil {
			return err
		}
		return c.DownloadKubeConfigs(ctx)
	}

	return nil
}

// shouldIncludeCluster determines if a cluster should be included based on configuration filters
func (c *NSKClient) shouldIncludeCluster(clusterName string) bool {
	// Check exclusion list first
	for _, excluded := range c.config.ExcludeClusters {
		if clusterName == excluded {
			return false
		}
	}

	// Check inclusion list (if specified)
	if len(c.config.IncludeClusters) > 0 {
		found := false
		for _, included := range c.config.IncludeClusters {
			if clusterName == included {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check cluster pattern (if specified)
	if c.config.ClusterPattern != "" {
		matched, err := regexp.MatchString(c.config.ClusterPattern, clusterName)
		if err != nil {
			c.logger.Error(err, "Invalid cluster pattern", "pattern", c.config.ClusterPattern)
			return false
		}
		return matched
	}

	return true
}

// detectEnvironment attempts to detect the environment based on cluster name
func (c *NSKClient) detectEnvironment(clusterName string) string {
	name := strings.ToLower(clusterName)

	if strings.Contains(name, "prod") || strings.Contains(name, "production") {
		return "production"
	}
	if strings.Contains(name, "stag") || strings.Contains(name, "staging") {
		return "staging"
	}
	if strings.Contains(name, "dev") || strings.Contains(name, "development") {
		return "development"
	}
	if strings.Contains(name, "test") || strings.Contains(name, "testing") {
		return "testing"
	}

	return "unknown"
}

// buildEnvironment builds the environment variables for NSK command execution
func (c *NSKClient) buildEnvironment() []string {
	env := os.Environ()

	// Add NSK-specific environment variables
	if c.config.RancherURL != "" {
		env = append(env, fmt.Sprintf("RANCHER_URL=%s", c.config.RancherURL))
	}
	if c.config.RancherToken != "" {
		env = append(env, fmt.Sprintf("RANCHER_TOKEN=%s", c.config.RancherToken))
	}
	if c.config.Profile != "" {
		env = append(env, fmt.Sprintf("NSK_PROFILE=%s", c.config.Profile))
	}
	if c.config.ConfigDir != "" {
		env = append(env, fmt.Sprintf("NSK_CONFIG_DIR=%s", c.config.ConfigDir))
	}

	// Add custom environment variables from configuration
	for key, value := range c.config.Environment {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	return env
}

// Close properly closes the NSK client and cleans up resources
func (c *NSKClient) Close() error {
	// Cancel context to stop any running operations
	if c.cancel != nil {
		c.cancel()
	}

	// Run all cleanup functions
	var errors []error
	for _, cleanup := range c.cleanupFuncs {
		if err := cleanup(); err != nil {
			errors = append(errors, err)
		}
	}

	// Clear clusters map
	c.mutex.Lock()
	c.clusters = make(map[string]*ClusterInfo)
	c.mutex.Unlock()

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %v", errors)
	}

	return nil
}

// RegisterCleanup registers a cleanup function to be called when the client is closed
func (c *NSKClient) RegisterCleanup(cleanup func() error) {
	c.cleanupFuncs = append(c.cleanupFuncs, cleanup)
}

// GetContext returns the client's context for long-running operations
func (c *NSKClient) GetContext() context.Context {
	return c.ctx
}

// IsHealthy performs a basic health check of the NSK integration
func (c *NSKClient) IsHealthy(ctx context.Context) error {
	// Check if NSK binary is available
	if _, err := exec.LookPath(c.nskPath); err != nil {
		return fmt.Errorf("NSK binary not available: %w", err)
	}

	// Try to execute a simple NSK command
	cmd := exec.CommandContext(ctx, c.nskPath, "version")
	env := c.buildEnvironment()
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("NSK command execution failed: %w", err)
	}

	return nil
}
