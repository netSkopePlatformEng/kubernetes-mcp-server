package kubernetes

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
)

// SimplifiedManager provides a simpler, more direct approach to Kubernetes client management.
// It uses clientcmd.BuildConfigFromFlags directly to avoid complex loading rules that might
// interfere with Rancher proxy endpoints.
type SimplifiedManager struct {
	// Cluster identification
	clusterName    string
	kubeconfigPath string

	// Core clients
	kubeClient      kubernetes.Interface
	dynamicClient   *dynamic.DynamicClient // Use concrete type to match Manager struct
	discoveryClient discovery.CachedDiscoveryInterface
	restMapper      meta.RESTMapper

	// Configuration
	restConfig   *rest.Config
	staticConfig *config.StaticConfig

	// Resource management
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	closed bool

	// Access control (reuse existing implementation)
	accessControlClientSet  *AccessControlClientset
	accessControlRESTMapper *AccessControlRESTMapper

	logger klog.Logger
}

// NewSimplifiedManager creates a new simplified manager for a specific cluster.
// This implementation uses direct config building to better preserve Rancher proxy paths.
func NewSimplifiedManager(clusterName, kubeconfigPath string, staticConfig *config.StaticConfig, logger klog.Logger) (*SimplifiedManager, error) {
	if kubeconfigPath == "" {
		return nil, fmt.Errorf("kubeconfig path is required")
	}

	logger.Info("Creating simplified manager",
		"cluster", clusterName,
		"kubeconfig", kubeconfigPath)

	// Build config directly from the kubeconfig file
	// This approach is simpler and more likely to preserve Rancher proxy paths correctly
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig %s: %w", kubeconfigPath, err)
	}

	// Set user agent if not already set
	if restConfig.UserAgent == "" {
		restConfig.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	// Log the server we're connecting to
	logger.Info("Built REST config",
		"cluster", clusterName,
		"server", restConfig.Host,
		"apiPath", restConfig.APIPath,
		"hasAuth", restConfig.BearerToken != "" || restConfig.BearerTokenFile != "")

	// Create the typed Kubernetes client
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create the dynamic client
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create discovery client with memory cache
	discoveryClient := memory.NewMemCacheClient(kubeClient.Discovery())

	// Create REST mapper for resource discovery
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)

	// Create access control clients if static config is provided
	var accessControlClientSet *AccessControlClientset
	var accessControlRESTMapper *AccessControlRESTMapper

	if staticConfig != nil {
		accessControlClientSet, err = NewAccessControlClientset(restConfig, staticConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create access control clientset: %w", err)
		}

		accessControlRESTMapper = NewAccessControlRESTMapper(restMapper, staticConfig)
	}

	ctx, cancel := context.WithCancel(context.Background())

	mgr := &SimplifiedManager{
		clusterName:             clusterName,
		kubeconfigPath:          kubeconfigPath,
		kubeClient:              kubeClient,
		dynamicClient:           dynamicClient,
		discoveryClient:         discoveryClient,
		restMapper:              restMapper,
		restConfig:              restConfig,
		staticConfig:            staticConfig,
		accessControlClientSet:  accessControlClientSet,
		accessControlRESTMapper: accessControlRESTMapper,
		ctx:                     ctx,
		cancel:                  cancel,
		logger:                  logger,
	}

	// Validate the connection
	if err := mgr.validateConnection(); err != nil {
		mgr.Close()
		return nil, fmt.Errorf("failed to validate connection to cluster %s: %w", clusterName, err)
	}

	logger.Info("Successfully created simplified manager", "cluster", clusterName)
	return mgr, nil
}

// validateConnection performs a basic connectivity check
func (mgr *SimplifiedManager) validateConnection() error {
	mgr.logger.V(2).Info("Validating connection", "cluster", mgr.clusterName)

	// Try to get server version as a basic connectivity test
	version, err := mgr.discoveryClient.ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to get server version: %w", err)
	}

	mgr.logger.Info("Connection validated",
		"cluster", mgr.clusterName,
		"version", version.GitVersion)

	return nil
}

// GetClusterName returns the name of the cluster this manager is connected to
func (mgr *SimplifiedManager) GetClusterName() string {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.clusterName
}

// GetKubeconfigPath returns the path to the kubeconfig file
func (mgr *SimplifiedManager) GetKubeconfigPath() string {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.kubeconfigPath
}

// ToRESTConfig returns the REST configuration
func (mgr *SimplifiedManager) ToRESTConfig() (*rest.Config, error) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.closed {
		return nil, fmt.Errorf("manager is closed")
	}

	return mgr.restConfig, nil
}

// ToDiscoveryClient returns the cached discovery client
func (mgr *SimplifiedManager) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.closed {
		return nil, fmt.Errorf("manager is closed")
	}

	return mgr.discoveryClient, nil
}

// ToRESTMapper returns the REST mapper for resource discovery
func (mgr *SimplifiedManager) ToRESTMapper() (meta.RESTMapper, error) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.closed {
		return nil, fmt.Errorf("manager is closed")
	}

	if mgr.accessControlRESTMapper != nil {
		return mgr.accessControlRESTMapper, nil
	}

	return mgr.restMapper, nil
}

// GetDynamicClient returns the dynamic client
func (mgr *SimplifiedManager) GetDynamicClient() (*dynamic.DynamicClient, error) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.closed {
		return nil, fmt.Errorf("manager is closed")
	}

	return mgr.dynamicClient, nil
}

// GetKubeClient returns the typed Kubernetes client
func (mgr *SimplifiedManager) GetKubeClient() (kubernetes.Interface, error) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.closed {
		return nil, fmt.Errorf("manager is closed")
	}

	// Return the raw kubeClient, as accessControlClientSet is a wrapper
	// that doesn't implement all methods of kubernetes.Interface
	return mgr.kubeClient, nil
}

// GetContext returns the manager's context
func (mgr *SimplifiedManager) GetContext() context.Context {
	return mgr.ctx
}

// Close cleans up all resources associated with this manager
func (mgr *SimplifiedManager) Close() error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if mgr.closed {
		return nil
	}

	mgr.logger.V(2).Info("Closing simplified manager", "cluster", mgr.clusterName)

	// Cancel context to stop any ongoing operations
	if mgr.cancel != nil {
		mgr.cancel()
	}

	// Invalidate discovery cache
	if mgr.discoveryClient != nil {
		mgr.discoveryClient.Invalidate()
	}

	mgr.closed = true
	mgr.logger.Info("Closed simplified manager", "cluster", mgr.clusterName)

	return nil
}

// IsClosed returns whether the manager has been closed
func (mgr *SimplifiedManager) IsClosed() bool {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.closed
}

// ConvertToManager converts this SimplifiedManager to a Manager interface for compatibility
// This allows us to use SimplifiedManager with existing code that expects a Manager
func (mgr *SimplifiedManager) ConvertToManager() *Manager {
	// Create a Manager struct that wraps our simplified implementation
	return &Manager{
		cfg:                     mgr.restConfig,
		discoveryClient:         mgr.discoveryClient,
		accessControlClientSet:  mgr.accessControlClientSet,
		accessControlRESTMapper: mgr.accessControlRESTMapper,
		dynamicClient:           mgr.dynamicClient, // Already the correct type
		staticConfig:            mgr.staticConfig,
		ctx:                     mgr.ctx,
		cancel:                  mgr.cancel,
		// Note: clientCmdConfig is not set as we don't use it in simplified approach
	}
}
