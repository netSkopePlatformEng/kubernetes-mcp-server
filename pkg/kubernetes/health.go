package kubernetes

import (
	"context"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// ClusterHealthState represents the health state of a cluster
type ClusterHealthState int

const (
	ClusterHealthUnknown ClusterHealthState = iota
	ClusterHealthHealthy
	ClusterHealthDegraded
	ClusterHealthUnhealthy
)

func (s ClusterHealthState) String() string {
	switch s {
	case ClusterHealthHealthy:
		return "healthy"
	case ClusterHealthDegraded:
		return "degraded"
	case ClusterHealthUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

// CircuitBreakerState represents the state of the circuit breaker
type CircuitBreakerState int

const (
	CircuitBreakerClosed CircuitBreakerState = iota
	CircuitBreakerOpen
	CircuitBreakerHalfOpen
)

func (s CircuitBreakerState) String() string {
	switch s {
	case CircuitBreakerClosed:
		return "closed"
	case CircuitBreakerOpen:
		return "open"
	case CircuitBreakerHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ClusterHealthConfig configuration for cluster health monitoring
type ClusterHealthConfig struct {
	MaxFailures     int           // Maximum failures before opening circuit
	CooldownTime    time.Duration // Time to wait before attempting half-open
	HealthCheckTime time.Duration // Interval between health checks
	RequestTimeout  time.Duration // Timeout for health check requests
}

// DefaultClusterHealthConfig returns default health configuration
func DefaultClusterHealthConfig() ClusterHealthConfig {
	return ClusterHealthConfig{
		MaxFailures:     3,
		CooldownTime:    30 * time.Second,
		HealthCheckTime: 10 * time.Second,
		RequestTimeout:  5 * time.Second,
	}
}

// ClusterHealthStatus contains the health status of a cluster
type ClusterHealthStatus struct {
	Cluster      string              `json:"cluster"`
	State        ClusterHealthState  `json:"state"`
	CircuitState CircuitBreakerState `json:"circuit_state"`
	FailureCount int                 `json:"failure_count"`
	LastCheck    time.Time           `json:"last_check"`
	LastFailure  time.Time           `json:"last_failure,omitempty"`
	LastSuccess  time.Time           `json:"last_success,omitempty"`
	ErrorMessage string              `json:"error_message,omitempty"`
}

// ClusterHealthMonitor monitors cluster health with circuit breaker pattern
type ClusterHealthMonitor struct {
	config      ClusterHealthConfig
	statuses    map[string]*ClusterHealthStatus
	mutex       sync.RWMutex
	logger      klog.Logger
	stopCh      chan struct{}
	healthCheck func(ctx context.Context, cluster string) error
}

// NewClusterHealthMonitor creates a new cluster health monitor
func NewClusterHealthMonitor(config ClusterHealthConfig, logger klog.Logger, healthCheck func(ctx context.Context, cluster string) error) *ClusterHealthMonitor {
	return &ClusterHealthMonitor{
		config:      config,
		statuses:    make(map[string]*ClusterHealthStatus),
		logger:      logger,
		stopCh:      make(chan struct{}),
		healthCheck: healthCheck,
	}
}

// Start starts the health monitoring
func (chm *ClusterHealthMonitor) Start(ctx context.Context, clusters []string) {
	chm.logger.Info("Starting cluster health monitor", "clusters", len(clusters))

	// Initialize status for each cluster
	chm.mutex.Lock()
	for _, cluster := range clusters {
		if _, exists := chm.statuses[cluster]; !exists {
			chm.statuses[cluster] = &ClusterHealthStatus{
				Cluster:      cluster,
				State:        ClusterHealthUnknown,
				CircuitState: CircuitBreakerClosed,
			}
		}
	}
	chm.mutex.Unlock()

	// Start monitoring goroutine
	go chm.monitorLoop(ctx)
}

// Stop stops the health monitoring
func (chm *ClusterHealthMonitor) Stop() {
	chm.logger.Info("Stopping cluster health monitor")
	select {
	case <-chm.stopCh:
		// Already stopped
		return
	default:
		close(chm.stopCh)
	}
}

// monitorLoop runs the health monitoring loop
func (chm *ClusterHealthMonitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(chm.config.HealthCheckTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-chm.stopCh:
			return
		case <-ticker.C:
			chm.checkAllClusters(ctx)
		}
	}
}

// checkAllClusters performs health checks on all monitored clusters
func (chm *ClusterHealthMonitor) checkAllClusters(ctx context.Context) {
	chm.mutex.RLock()
	clusters := make([]string, 0, len(chm.statuses))
	for cluster := range chm.statuses {
		clusters = append(clusters, cluster)
	}
	chm.mutex.RUnlock()

	for _, cluster := range clusters {
		chm.checkClusterHealth(ctx, cluster)
	}
}

// checkClusterHealth performs a health check on a specific cluster
func (chm *ClusterHealthMonitor) checkClusterHealth(ctx context.Context, cluster string) {
	chm.mutex.Lock()
	status, exists := chm.statuses[cluster]
	if !exists {
		chm.mutex.Unlock()
		return
	}

	// Skip if circuit is open and cooldown hasn't expired
	if status.CircuitState == CircuitBreakerOpen {
		if time.Since(status.LastFailure) < chm.config.CooldownTime {
			chm.mutex.Unlock()
			return
		}
		// Move to half-open state
		status.CircuitState = CircuitBreakerHalfOpen
		chm.logger.V(2).Info("Circuit breaker moving to half-open", "cluster", cluster)
	}
	chm.mutex.Unlock()

	// Perform health check with timeout
	checkCtx, cancel := context.WithTimeout(ctx, chm.config.RequestTimeout)
	defer cancel()

	err := chm.healthCheck(checkCtx, cluster)

	chm.mutex.Lock()
	defer chm.mutex.Unlock()

	status.LastCheck = time.Now()

	if err != nil {
		// Health check failed
		status.FailureCount++
		status.LastFailure = time.Now()
		status.ErrorMessage = err.Error()

		chm.logger.V(3).Info("Cluster health check failed",
			"cluster", cluster,
			"failures", status.FailureCount,
			"error", err.Error())

		// Update state based on failure count
		if status.FailureCount >= chm.config.MaxFailures {
			status.State = ClusterHealthUnhealthy
			status.CircuitState = CircuitBreakerOpen
			chm.logger.Info("Circuit breaker opened for cluster",
				"cluster", cluster,
				"failures", status.FailureCount)
		} else {
			status.State = ClusterHealthDegraded
		}

		// If in half-open state and failed, go back to open
		if status.CircuitState == CircuitBreakerHalfOpen {
			status.CircuitState = CircuitBreakerOpen
			chm.logger.V(2).Info("Circuit breaker reopened from half-open", "cluster", cluster)
		}
	} else {
		// Health check succeeded
		status.LastSuccess = time.Now()
		status.ErrorMessage = ""

		if status.CircuitState == CircuitBreakerHalfOpen || status.FailureCount > 0 {
			chm.logger.Info("Cluster health restored", "cluster", cluster)
		}

		// Reset failure count and update state
		status.FailureCount = 0
		status.State = ClusterHealthHealthy
		status.CircuitState = CircuitBreakerClosed
	}
}

// IsHealthy returns true if the cluster is healthy and circuit is closed
func (chm *ClusterHealthMonitor) IsHealthy(cluster string) bool {
	chm.mutex.RLock()
	defer chm.mutex.RUnlock()

	status, exists := chm.statuses[cluster]
	if !exists {
		return false
	}

	return status.State == ClusterHealthHealthy && status.CircuitState == CircuitBreakerClosed
}

// GetClusterStatus returns the health status of a specific cluster
func (chm *ClusterHealthMonitor) GetClusterStatus(cluster string) (*ClusterHealthStatus, bool) {
	chm.mutex.RLock()
	defer chm.mutex.RUnlock()

	status, exists := chm.statuses[cluster]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid data races
	statusCopy := *status
	return &statusCopy, true
}

// GetAllStatuses returns the health status of all monitored clusters
func (chm *ClusterHealthMonitor) GetAllStatuses() map[string]ClusterHealthStatus {
	chm.mutex.RLock()
	defer chm.mutex.RUnlock()

	statuses := make(map[string]ClusterHealthStatus)
	for cluster, status := range chm.statuses {
		statuses[cluster] = *status // Copy the status
	}

	return statuses
}

// AddCluster adds a new cluster to monitoring
func (chm *ClusterHealthMonitor) AddCluster(cluster string) {
	chm.mutex.Lock()
	defer chm.mutex.Unlock()

	if _, exists := chm.statuses[cluster]; !exists {
		chm.statuses[cluster] = &ClusterHealthStatus{
			Cluster:      cluster,
			State:        ClusterHealthUnknown,
			CircuitState: CircuitBreakerClosed,
		}
		chm.logger.V(2).Info("Added cluster to health monitoring", "cluster", cluster)
	}
}

// RemoveCluster removes a cluster from monitoring
func (chm *ClusterHealthMonitor) RemoveCluster(cluster string) {
	chm.mutex.Lock()
	defer chm.mutex.Unlock()

	if _, exists := chm.statuses[cluster]; exists {
		delete(chm.statuses, cluster)
		chm.logger.V(2).Info("Removed cluster from health monitoring", "cluster", cluster)
	}
}

// GetHealthySummary returns a summary of healthy vs unhealthy clusters
func (chm *ClusterHealthMonitor) GetHealthySummary() (healthy, total int) {
	chm.mutex.RLock()
	defer chm.mutex.RUnlock()

	total = len(chm.statuses)
	for _, status := range chm.statuses {
		if status.State == ClusterHealthHealthy && status.CircuitState == CircuitBreakerClosed {
			healthy++
		}
	}

	return healthy, total
}
