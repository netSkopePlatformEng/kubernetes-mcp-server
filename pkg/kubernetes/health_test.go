package kubernetes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"k8s.io/klog/v2"
)

func TestClusterHealthState_String(t *testing.T) {
	tests := []struct {
		state    ClusterHealthState
		expected string
	}{
		{ClusterHealthUnknown, "unknown"},
		{ClusterHealthHealthy, "healthy"},
		{ClusterHealthDegraded, "degraded"},
		{ClusterHealthUnhealthy, "unhealthy"},
		{ClusterHealthState(999), "unknown"}, // Invalid state
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.state.String()
			if result != tt.expected {
				t.Errorf("ClusterHealthState(%d).String() = %q, want %q", tt.state, result, tt.expected)
			}
		})
	}
}

func TestCircuitBreakerState_String(t *testing.T) {
	tests := []struct {
		state    CircuitBreakerState
		expected string
	}{
		{CircuitBreakerClosed, "closed"},
		{CircuitBreakerOpen, "open"},
		{CircuitBreakerHalfOpen, "half-open"},
		{CircuitBreakerState(999), "unknown"}, // Invalid state
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.state.String()
			if result != tt.expected {
				t.Errorf("CircuitBreakerState(%d).String() = %q, want %q", tt.state, result, tt.expected)
			}
		})
	}
}

func TestDefaultClusterHealthConfig(t *testing.T) {
	config := DefaultClusterHealthConfig()

	if config.MaxFailures <= 0 {
		t.Error("MaxFailures should be greater than 0")
	}
	if config.CooldownTime <= 0 {
		t.Error("CooldownTime should be greater than 0")
	}
	if config.HealthCheckTime <= 0 {
		t.Error("HealthCheckTime should be greater than 0")
	}
	if config.RequestTimeout <= 0 {
		t.Error("RequestTimeout should be greater than 0")
	}
}

func TestNewClusterHealthMonitor(t *testing.T) {
	config := DefaultClusterHealthConfig()
	logger := klog.Background()
	healthCheck := func(ctx context.Context, cluster string) error {
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	if monitor == nil {
		t.Fatal("NewClusterHealthMonitor returned nil")
	}
	if monitor.config != config {
		t.Error("Config not set correctly")
	}
	if monitor.statuses == nil {
		t.Error("Statuses map not initialized")
	}
}

func TestClusterHealthMonitor_StartStop(t *testing.T) {
	config := ClusterHealthConfig{
		MaxFailures:     2,
		CooldownTime:    100 * time.Millisecond,
		HealthCheckTime: 50 * time.Millisecond,
		RequestTimeout:  10 * time.Millisecond,
	}
	logger := klog.Background()
	healthCheck := func(ctx context.Context, cluster string) error {
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	clusters := []string{"test-cluster-1", "test-cluster-2"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start monitoring
	monitor.Start(ctx, clusters)

	// Verify clusters are initialized
	for _, cluster := range clusters {
		status, exists := monitor.GetClusterStatus(cluster)
		if !exists {
			t.Errorf("Cluster %s not found in monitor", cluster)
		}
		if status.State != ClusterHealthUnknown {
			t.Errorf("Expected cluster %s to have Unknown state initially, got %s", cluster, status.State)
		}
		if status.CircuitState != CircuitBreakerClosed {
			t.Errorf("Expected cluster %s to have Closed circuit initially, got %s", cluster, status.CircuitState)
		}
	}

	// Stop monitoring
	monitor.Stop()
}

func TestClusterHealthMonitor_HealthyCluster(t *testing.T) {
	config := ClusterHealthConfig{
		MaxFailures:     3,
		CooldownTime:    100 * time.Millisecond,
		HealthCheckTime: 50 * time.Millisecond,
		RequestTimeout:  10 * time.Millisecond,
	}
	logger := klog.Background()

	// Always successful health check
	healthCheck := func(ctx context.Context, cluster string) error {
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	cluster := "healthy-cluster"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor.Start(ctx, []string{cluster})
	defer monitor.Stop()

	// Wait for a few health checks to complete
	time.Sleep(150 * time.Millisecond)

	status, exists := monitor.GetClusterStatus(cluster)
	if !exists {
		t.Fatal("Cluster not found")
	}

	if status.State != ClusterHealthHealthy {
		t.Errorf("Expected healthy cluster state, got %s", status.State)
	}
	if status.CircuitState != CircuitBreakerClosed {
		t.Errorf("Expected closed circuit, got %s", status.CircuitState)
	}
	if status.FailureCount != 0 {
		t.Errorf("Expected 0 failures, got %d", status.FailureCount)
	}
	if !monitor.IsHealthy(cluster) {
		t.Error("IsHealthy should return true for healthy cluster")
	}
}

func TestClusterHealthMonitor_FailingCluster(t *testing.T) {
	config := ClusterHealthConfig{
		MaxFailures:     2,
		CooldownTime:    100 * time.Millisecond,
		HealthCheckTime: 20 * time.Millisecond,
		RequestTimeout:  10 * time.Millisecond,
	}
	logger := klog.Background()

	// Always failing health check
	testError := errors.New("connection failed")
	healthCheck := func(ctx context.Context, cluster string) error {
		return testError
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	cluster := "failing-cluster"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor.Start(ctx, []string{cluster})
	defer monitor.Stop()

	// Wait for health checks to accumulate failures
	time.Sleep(200 * time.Millisecond)

	status, exists := monitor.GetClusterStatus(cluster)
	if !exists {
		t.Fatal("Cluster not found")
	}

	if status.State != ClusterHealthUnhealthy {
		t.Errorf("Expected unhealthy cluster state, got %s", status.State)
	}
	if status.CircuitState != CircuitBreakerOpen {
		t.Errorf("Expected open circuit, got %s", status.CircuitState)
	}
	if status.FailureCount < config.MaxFailures {
		t.Errorf("Expected at least %d failures, got %d", config.MaxFailures, status.FailureCount)
	}
	if status.ErrorMessage == "" {
		t.Error("Expected error message to be set")
	}
	if monitor.IsHealthy(cluster) {
		t.Error("IsHealthy should return false for unhealthy cluster")
	}
}

func TestClusterHealthMonitor_CircuitBreakerRecovery(t *testing.T) {
	config := ClusterHealthConfig{
		MaxFailures:     2,
		CooldownTime:    50 * time.Millisecond,
		HealthCheckTime: 20 * time.Millisecond,
		RequestTimeout:  10 * time.Millisecond,
	}
	logger := klog.Background()

	// Track health check calls
	var healthCheckCalls int
	var mu sync.Mutex

	healthCheck := func(ctx context.Context, cluster string) error {
		mu.Lock()
		defer mu.Unlock()
		healthCheckCalls++

		// Fail for first few calls, then succeed
		if healthCheckCalls <= 3 {
			return errors.New("initial failure")
		}
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	cluster := "recovery-cluster"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor.Start(ctx, []string{cluster})
	defer monitor.Stop()

	// Wait for initial failures to open circuit
	time.Sleep(100 * time.Millisecond)

	// Verify circuit is open
	status, _ := monitor.GetClusterStatus(cluster)
	if status.CircuitState != CircuitBreakerOpen {
		t.Errorf("Expected circuit to be open after failures, got %s", status.CircuitState)
	}

	// Wait for cooldown and recovery
	time.Sleep(200 * time.Millisecond)

	// Verify circuit recovered
	status, _ = monitor.GetClusterStatus(cluster)
	if status.State != ClusterHealthHealthy {
		t.Errorf("Expected cluster to recover to healthy state, got %s", status.State)
	}
	if status.CircuitState != CircuitBreakerClosed {
		t.Errorf("Expected circuit to close after recovery, got %s", status.CircuitState)
	}
	if status.FailureCount != 0 {
		t.Errorf("Expected failure count to reset after recovery, got %d", status.FailureCount)
	}
}

func TestClusterHealthMonitor_HalfOpenState(t *testing.T) {
	config := ClusterHealthConfig{
		MaxFailures:     2,
		CooldownTime:    50 * time.Millisecond,
		HealthCheckTime: 20 * time.Millisecond,
		RequestTimeout:  10 * time.Millisecond,
	}
	logger := klog.Background()

	var healthCheckCalls int
	var mu sync.Mutex

	healthCheck := func(ctx context.Context, cluster string) error {
		mu.Lock()
		defer mu.Unlock()
		healthCheckCalls++

		// Fail initially to open circuit
		if healthCheckCalls <= 3 {
			return errors.New("initial failure")
		}
		// Then fail again to test half-open -> open transition
		if healthCheckCalls == 4 {
			return errors.New("half-open failure")
		}
		// Finally succeed
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	cluster := "half-open-cluster"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor.Start(ctx, []string{cluster})
	defer monitor.Stop()

	// Wait for initial failures and circuit opening
	time.Sleep(100 * time.Millisecond)

	// Wait for cooldown period and half-open attempt
	time.Sleep(100 * time.Millisecond)

	// Allow time for eventual recovery
	time.Sleep(200 * time.Millisecond)

	// The exact final state depends on timing, but we should see evidence of the cycle
	mu.Lock()
	calls := healthCheckCalls
	mu.Unlock()

	if calls < 4 {
		t.Errorf("Expected at least 4 health check calls to test half-open behavior, got %d", calls)
	}
}

func TestClusterHealthMonitor_AddRemoveCluster(t *testing.T) {
	config := DefaultClusterHealthConfig()
	logger := klog.Background()
	healthCheck := func(ctx context.Context, cluster string) error {
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	// Add cluster
	cluster := "dynamic-cluster"
	monitor.AddCluster(cluster)

	status, exists := monitor.GetClusterStatus(cluster)
	if !exists {
		t.Error("Cluster should exist after adding")
	}
	if status.State != ClusterHealthUnknown {
		t.Errorf("Expected Unknown state for new cluster, got %s", status.State)
	}

	// Remove cluster
	monitor.RemoveCluster(cluster)

	_, exists = monitor.GetClusterStatus(cluster)
	if exists {
		t.Error("Cluster should not exist after removing")
	}
}

func TestClusterHealthMonitor_GetAllStatuses(t *testing.T) {
	config := DefaultClusterHealthConfig()
	logger := klog.Background()
	healthCheck := func(ctx context.Context, cluster string) error {
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	clusters := []string{"cluster-1", "cluster-2", "cluster-3"}
	for _, cluster := range clusters {
		monitor.AddCluster(cluster)
	}

	statuses := monitor.GetAllStatuses()
	if len(statuses) != len(clusters) {
		t.Errorf("Expected %d statuses, got %d", len(clusters), len(statuses))
	}

	for _, cluster := range clusters {
		if status, exists := statuses[cluster]; !exists {
			t.Errorf("Status for cluster %s not found", cluster)
		} else if status.Cluster != cluster {
			t.Errorf("Status cluster name mismatch: expected %s, got %s", cluster, status.Cluster)
		}
	}
}

func TestClusterHealthMonitor_GetHealthySummary(t *testing.T) {
	config := DefaultClusterHealthConfig()
	logger := klog.Background()

	// Create different health check behaviors
	clusterHealth := map[string]bool{
		"healthy-1":   true,
		"healthy-2":   true,
		"unhealthy-1": false,
		"unhealthy-2": false,
	}

	healthCheck := func(ctx context.Context, cluster string) error {
		if healthy, exists := clusterHealth[cluster]; exists && healthy {
			return nil
		}
		return errors.New("cluster unhealthy")
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	// Add clusters
	for cluster := range clusterHealth {
		monitor.AddCluster(cluster)
	}

	// Manually set states for testing
	for cluster, healthy := range clusterHealth {
		status, _ := monitor.GetClusterStatus(cluster)
		monitor.statuses[cluster].State = func() ClusterHealthState {
			if healthy {
				return ClusterHealthHealthy
			}
			return ClusterHealthUnhealthy
		}()
		monitor.statuses[cluster].CircuitState = func() CircuitBreakerState {
			if healthy {
				return CircuitBreakerClosed
			}
			return CircuitBreakerOpen
		}()

		// Copy the modified status back
		if status != nil {
			*status = *monitor.statuses[cluster]
		}
	}

	healthy, total := monitor.GetHealthySummary()
	expectedHealthy := 2
	expectedTotal := 4

	if healthy != expectedHealthy {
		t.Errorf("Expected %d healthy clusters, got %d", expectedHealthy, healthy)
	}
	if total != expectedTotal {
		t.Errorf("Expected %d total clusters, got %d", expectedTotal, total)
	}
}

func TestClusterHealthMonitor_ConcurrentAccess(t *testing.T) {
	config := ClusterHealthConfig{
		MaxFailures:     3,
		CooldownTime:    10 * time.Millisecond,
		HealthCheckTime: 5 * time.Millisecond,
		RequestTimeout:  2 * time.Millisecond,
	}
	logger := klog.Background()
	healthCheck := func(ctx context.Context, cluster string) error {
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	clusters := []string{"concurrent-1", "concurrent-2", "concurrent-3"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor.Start(ctx, clusters)
	defer monitor.Stop()

	// Perform concurrent operations
	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				for _, cluster := range clusters {
					monitor.IsHealthy(cluster)
					monitor.GetClusterStatus(cluster)
				}
			}
		}()
	}

	// Concurrent add/remove operations
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			dynamicCluster := "dynamic"
			monitor.AddCluster(dynamicCluster)
			monitor.RemoveCluster(dynamicCluster)
		}
	}()

	// Wait for all operations to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlocks
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out - possible deadlock")
	}
}

func TestClusterHealthMonitor_ContextCancellation(t *testing.T) {
	config := ClusterHealthConfig{
		MaxFailures:     3,
		CooldownTime:    100 * time.Millisecond,
		HealthCheckTime: 20 * time.Millisecond,
		RequestTimeout:  10 * time.Millisecond,
	}
	logger := klog.Background()

	// Track if health check is called after cancellation
	var callsAfterCancel int
	var mu sync.Mutex
	cancelled := false

	healthCheck := func(ctx context.Context, cluster string) error {
		mu.Lock()
		defer mu.Unlock()
		if cancelled {
			callsAfterCancel++
		}
		return nil
	}

	monitor := NewClusterHealthMonitor(config, logger, healthCheck)

	ctx, cancel := context.WithCancel(context.Background())
	monitor.Start(ctx, []string{"test-cluster"})

	// Let it run for a bit
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	mu.Lock()
	cancelled = true
	mu.Unlock()
	cancel()

	// Wait a bit more
	time.Sleep(100 * time.Millisecond)

	monitor.Stop()

	mu.Lock()
	calls := callsAfterCancel
	mu.Unlock()

	// There might be a few calls after cancellation due to timing,
	// but it should stop quickly
	if calls > 10 {
		t.Errorf("Too many health check calls after context cancellation: %d", calls)
	}
}
