package kubernetes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"k8s.io/klog/v2"
)

// NSKIntegration manages NSK (Netskope Kubernetes) tool integration
type NSKIntegration struct {
	config         *config.NSKConfig
	clusterManager *MultiClusterManager
	refreshTicker  *time.Ticker
	stopChan       chan struct{}
	logger         klog.Logger
	lastRefresh    time.Time
	nextRefresh    time.Time
	refreshHistory []RefreshResult
	mu             sync.RWMutex
}

// RefreshResult represents the result of a refresh operation
type RefreshResult struct {
	Timestamp time.Time
	Success   bool
	Error     error
	Clusters  []string
	Details   string
}

// NewNSKIntegrationForMCM creates a new NSK integration for MultiClusterManager
func NewNSKIntegrationForMCM(cfg *config.NSKConfig, mcm *MultiClusterManager) *NSKIntegration {
	return &NSKIntegration{
		config:         cfg,
		clusterManager: mcm,
		stopChan:       make(chan struct{}),
		logger:         klog.Background(),
		refreshHistory: make([]RefreshResult, 0, 100),
	}
}

// Start initializes the NSK integration and starts auto-refresh if enabled
func (nsk *NSKIntegration) Start(ctx context.Context) error {
	// Set environment variables
	if err := nsk.setEnvironment(); err != nil {
		return fmt.Errorf("failed to set NSK environment: %w", err)
	}

	// Ensure config directory exists
	if nsk.config.ConfigDir != "" {
		if err := os.MkdirAll(nsk.config.ConfigDir, 0700); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	// Initial refresh
	if err := nsk.RefreshKubeConfigs(ctx); err != nil {
		nsk.logger.Error(err, "Initial kubeconfig refresh failed")
		// Don't fail startup if initial refresh fails
	}

	// Start auto-refresh if enabled
	if nsk.config.AutoRefresh {
		interval, err := time.ParseDuration(nsk.config.RefreshInterval)
		if err != nil {
			interval = time.Hour // default to 1 hour
			nsk.logger.Info("Invalid refresh interval, using default", "interval", interval)
		}

		nsk.refreshTicker = time.NewTicker(interval)
		go nsk.refreshLoop(ctx)

		nsk.mu.Lock()
		nsk.nextRefresh = time.Now().Add(interval)
		nsk.mu.Unlock()
	}

	return nil
}

// Stop stops the NSK integration and cleanup resources
func (nsk *NSKIntegration) Stop() {
	if nsk.refreshTicker != nil {
		nsk.refreshTicker.Stop()
	}
	close(nsk.stopChan)
}

// setEnvironment sets NSK environment variables
func (nsk *NSKIntegration) setEnvironment() error {
	envVars := map[string]string{}

	// Set from config
	if nsk.config.RancherURL != "" {
		envVars["RANCHER_URL"] = nsk.config.RancherURL
	}
	if nsk.config.RancherToken != "" {
		envVars["RANCHER_TOKEN"] = nsk.config.RancherToken
	}
	if nsk.config.Profile != "" {
		envVars["NSK_PROFILE"] = nsk.config.Profile
	}
	if nsk.config.ConfigDir != "" {
		envVars["NSK_CONFDIR"] = nsk.config.ConfigDir
	}

	// Add custom environment variables
	for key, value := range nsk.config.Environment {
		envVars[key] = value
	}

	// Set environment variables
	for key, value := range envVars {
		if value != "" {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("failed to set %s: %w", key, err)
			}
		}
	}

	// Log environment setup (without sensitive values)
	nsk.logger.Info("NSK environment configured",
		"rancher_url", nsk.config.RancherURL,
		"profile", nsk.config.Profile,
		"config_dir", nsk.config.ConfigDir,
		"token_set", nsk.config.RancherToken != "")

	return nil
}

// RefreshKubeConfigs refreshes all kubeconfig files from Rancher via NSK
func (nsk *NSKIntegration) RefreshKubeConfigs(ctx context.Context) error {
	nsk.logger.Info("Refreshing kubeconfigs from Rancher via NSK")

	startTime := time.Now()
	result := RefreshResult{
		Timestamp: startTime,
		Success:   false,
	}

	// Build NSK command
	args := []string{"cluster", "kubeconfig"}

	// Always specify the config directory explicitly
	if nsk.config.ConfigDir != "" {
		args = append(args, "--confdir", nsk.config.ConfigDir)
	}

	// Add profile if specified
	if nsk.config.Profile != "" {
		args = append(args, "--profile", nsk.config.Profile)
	}

	// Add cluster pattern if specified
	if nsk.config.ClusterPattern != "" {
		args = append(args, "--name-pattern", nsk.config.ClusterPattern)
	}

	// Execute NSK command
	cmd := exec.CommandContext(ctx, nsk.getNSKPath(), args...)
	cmd.Dir = nsk.config.ConfigDir

	// Capture environment for the command
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		result.Error = fmt.Errorf("NSK kubeconfig refresh failed: %w, output: %s", err, output)
		nsk.addRefreshResult(result)
		return result.Error
	}

	nsk.logger.Info("NSK kubeconfig refresh completed", "output", string(output))

	// Parse output to get list of clusters
	result.Clusters = nsk.parseNSKOutput(string(output))
	result.Details = string(output)

	// Trigger cluster manager to rescan directory
	if err := nsk.clusterManager.RefreshClusters(ctx); err != nil {
		result.Error = fmt.Errorf("failed to rediscover clusters: %w", err)
		nsk.addRefreshResult(result)
		return result.Error
	}

	// Update refresh times
	nsk.mu.Lock()
	nsk.lastRefresh = startTime
	if nsk.config.AutoRefresh && nsk.refreshTicker != nil {
		interval, _ := time.ParseDuration(nsk.config.RefreshInterval)
		nsk.nextRefresh = time.Now().Add(interval)
	}
	nsk.mu.Unlock()

	result.Success = true
	nsk.addRefreshResult(result)

	nsk.logger.Info("Kubeconfig refresh successful",
		"clusters", len(result.Clusters),
		"duration", time.Since(startTime))

	return nil
}

// GetClusterKubeConfig gets kubeconfig for a specific cluster
func (nsk *NSKIntegration) GetClusterKubeConfig(ctx context.Context, clusterName string, save bool) (string, error) {
	nsk.logger.Info("Getting kubeconfig for cluster", "cluster", clusterName, "save", save)

	args := []string{"cluster", "kubeconfig", "--name", clusterName}

	// Always specify the config directory explicitly
	if nsk.config.ConfigDir != "" {
		args = append(args, "--confdir", nsk.config.ConfigDir)
	}

	// Add profile if specified
	if nsk.config.Profile != "" {
		args = append(args, "--profile", nsk.config.Profile)
	}

	if !save {
		// Output to stdout instead of saving to file
		args = append(args, "--stdout")
	}

	cmd := exec.CommandContext(ctx, nsk.getNSKPath(), args...)
	cmd.Dir = nsk.config.ConfigDir
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("NSK get kubeconfig failed for cluster %s: %w, output: %s",
			clusterName, err, output)
	}

	if save {
		nsk.logger.Info("NSK kubeconfig saved for cluster", "cluster", clusterName)
		// Trigger cluster manager to rescan directory to pick up new file
		if err := nsk.clusterManager.RefreshClusters(ctx); err != nil {
			nsk.logger.Error(err, "Failed to rediscover clusters after kubeconfig save")
		}
	}

	return string(output), nil
}

// refreshLoop runs the auto-refresh loop
func (nsk *NSKIntegration) refreshLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-nsk.stopChan:
			return
		case <-nsk.refreshTicker.C:
			if err := nsk.RefreshKubeConfigs(ctx); err != nil {
				nsk.logger.Error(err, "Auto-refresh of kubeconfigs failed")
			}
		}
	}
}

// getNSKPath returns the path to the NSK binary
func (nsk *NSKIntegration) getNSKPath() string {
	if nsk.config.NSKPath != "" {
		return nsk.config.NSKPath
	}
	return "nsk" // default to PATH lookup
}

// parseNSKOutput parses the NSK command output to extract cluster names
func (nsk *NSKIntegration) parseNSKOutput(output string) []string {
	var clusters []string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Look for lines that indicate a cluster was processed
		// This is a simplified parser - adjust based on actual NSK output format
		if strings.Contains(line, "kubeconfig saved") || strings.Contains(line, "cluster:") {
			// Extract cluster name from the line
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "cluster:" && i+1 < len(parts) {
					clusters = append(clusters, parts[i+1])
				}
			}
		}
	}

	return clusters
}

// addRefreshResult adds a refresh result to history
func (nsk *NSKIntegration) addRefreshResult(result RefreshResult) {
	nsk.mu.Lock()
	defer nsk.mu.Unlock()

	nsk.refreshHistory = append(nsk.refreshHistory, result)

	// Keep only last 100 results
	if len(nsk.refreshHistory) > 100 {
		nsk.refreshHistory = nsk.refreshHistory[len(nsk.refreshHistory)-100:]
	}
}

// GetLastRefreshTime returns the last refresh time
func (nsk *NSKIntegration) GetLastRefreshTime() time.Time {
	nsk.mu.RLock()
	defer nsk.mu.RUnlock()
	return nsk.lastRefresh
}

// GetNextRefreshTime returns the next scheduled refresh time
func (nsk *NSKIntegration) GetNextRefreshTime() time.Time {
	nsk.mu.RLock()
	defer nsk.mu.RUnlock()
	return nsk.nextRefresh
}

// GetRefreshHistory returns the refresh history
func (nsk *NSKIntegration) GetRefreshHistory() []RefreshResult {
	nsk.mu.RLock()
	defer nsk.mu.RUnlock()

	// Return a copy to avoid race conditions
	history := make([]RefreshResult, len(nsk.refreshHistory))
	copy(history, nsk.refreshHistory)
	return history
}

// GetStatus returns the current NSK integration status
func (nsk *NSKIntegration) GetStatus() map[string]interface{} {
	nsk.mu.RLock()
	defer nsk.mu.RUnlock()

	status := map[string]interface{}{
		"enabled":      nsk.config.Enabled,
		"profile":      nsk.config.Profile,
		"rancher_url":  nsk.config.RancherURL,
		"config_dir":   nsk.config.ConfigDir,
		"auto_refresh": nsk.config.AutoRefresh,
		"last_refresh": nsk.lastRefresh,
		"next_refresh": nsk.nextRefresh,
	}

	// Add last refresh result
	if len(nsk.refreshHistory) > 0 {
		lastResult := nsk.refreshHistory[len(nsk.refreshHistory)-1]
		status["last_result"] = map[string]interface{}{
			"success":   lastResult.Success,
			"timestamp": lastResult.Timestamp,
			"clusters":  lastResult.Clusters,
			"error":     "",
		}
		if lastResult.Error != nil {
			status["last_result"].(map[string]interface{})["error"] = lastResult.Error.Error()
		}
	}

	return status
}

// ValidateNSKBinary validates that the NSK binary is available and executable
func (nsk *NSKIntegration) ValidateNSKBinary() error {
	nskPath := nsk.getNSKPath()

	// Check if binary exists
	fullPath, err := exec.LookPath(nskPath)
	if err != nil {
		return fmt.Errorf("NSK binary not found: %s", nskPath)
	}

	// Check if executable
	if info, err := os.Stat(fullPath); err == nil {
		if info.Mode()&0111 == 0 {
			return fmt.Errorf("NSK binary not executable: %s", fullPath)
		}
	}

	// Try to run version command to validate it works
	cmd := exec.Command(fullPath, "version")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("NSK binary validation failed: %w, output: %s", err, output)
	}

	nsk.logger.Info("NSK binary validated", "path", fullPath)
	return nil
}

// DiscoverClusters discovers new clusters from Rancher
func (nsk *NSKIntegration) DiscoverClusters(ctx context.Context, pattern string) ([]string, error) {
	args := []string{"cluster", "list"}

	// Always specify the config directory explicitly
	if nsk.config.ConfigDir != "" {
		args = append(args, "--confdir", nsk.config.ConfigDir)
	}

	// Add profile if specified
	if nsk.config.Profile != "" {
		args = append(args, "--profile", nsk.config.Profile)
	}

	if pattern != "" {
		args = append(args, "--name-pattern", pattern)
	}

	cmd := exec.CommandContext(ctx, nsk.getNSKPath(), args...)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("NSK cluster discovery failed: %w, output: %s", err, output)
	}

	// Parse output to get cluster names
	clusters := nsk.parseClusterList(string(output))
	return clusters, nil
}

// parseClusterList parses the NSK cluster list output
func (nsk *NSKIntegration) parseClusterList(output string) []string {
	var clusters []string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Simple parsing - adjust based on actual NSK output format
		// Assuming one cluster name per line
		clusters = append(clusters, line)
	}

	return clusters
}
