package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/fsnotify/fsnotify"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/helm"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

type HeaderKey string

const (
	CustomAuthorizationHeader = HeaderKey("kubernetes-authorization")
	OAuthAuthorizationHeader  = HeaderKey("Authorization")

	CustomUserAgent = "kubernetes-mcp-server/bearer-token-auth"
)

type CloseWatchKubeConfig func() error

type Kubernetes struct {
	manager                   *Manager
	multiClusterManager       *MultiClusterManager
	simpleMultiClusterManager *SimpleMultiClusterManager
	freshMultiClusterManager  *FreshMultiClusterManager
}

type Manager struct {
	cfg                     *rest.Config
	clientCmdConfig         clientcmd.ClientConfig
	discoveryClient         discovery.CachedDiscoveryInterface
	accessControlClientSet  *AccessControlClientset
	accessControlRESTMapper *AccessControlRESTMapper
	dynamicClient           *dynamic.DynamicClient

	staticConfig         *config.StaticConfig
	CloseWatchKubeConfig CloseWatchKubeConfig

	// Resource cleanup
	ctx          context.Context
	cancel       context.CancelFunc
	cleanupFuncs []func() error
	closed       bool
	closedMu     sync.Mutex
}

var Scheme = scheme.Scheme
var ParameterCodec = runtime.NewParameterCodec(Scheme)

var _ helm.Kubernetes = &Manager{}

func NewManager(config *config.StaticConfig) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	k8s := &Manager{
		staticConfig: config,
		ctx:          ctx,
		cancel:       cancel,
		cleanupFuncs: make([]func() error, 0),
	}

	// Log the kubeconfig being used
	if config.KubeConfig != "" {
		klog.V(2).Infof("Creating manager with kubeconfig: %s", config.KubeConfig)
	} else {
		klog.V(2).Info("Creating manager with default kubeconfig or in-cluster config")
	}

	if err := resolveKubernetesConfigurations(k8s); err != nil {
		return nil, err
	}
	// TODO: Won't work because not all client-go clients use the shared context (e.g. discovery client uses context.TODO())
	//k8s.cfg.Wrap(func(original http.RoundTripper) http.RoundTripper {
	//	return &impersonateRoundTripper{original}
	//})
	var err error
	k8s.accessControlClientSet, err = NewAccessControlClientset(k8s.cfg, k8s.staticConfig)
	if err != nil {
		return nil, err
	}
	k8s.discoveryClient = memory.NewMemCacheClient(k8s.accessControlClientSet.DiscoveryClient())
	k8s.accessControlRESTMapper = NewAccessControlRESTMapper(
		restmapper.NewDeferredDiscoveryRESTMapper(k8s.discoveryClient),
		k8s.staticConfig,
	)
	k8s.dynamicClient, err = dynamic.NewForConfig(k8s.cfg)
	if err != nil {
		return nil, err
	}

	// Log authentication status for debugging
	if k8s.cfg != nil {
		if k8s.cfg.BearerToken == "" && k8s.cfg.BearerTokenFile == "" &&
			k8s.cfg.Username == "" && k8s.cfg.Password == "" &&
			k8s.cfg.CertFile == "" && k8s.cfg.KeyFile == "" &&
			len(k8s.cfg.CertData) == 0 && len(k8s.cfg.KeyData) == 0 &&
			k8s.cfg.ExecProvider == nil && k8s.cfg.AuthProvider == nil {
			klog.V(1).Infof("Manager created with no authentication for server: %s", k8s.cfg.Host)
		} else {
			klog.V(3).Infof("Manager created with authentication for server: %s", k8s.cfg.Host)
		}
	}

	return k8s, nil
}

// NewKubernetes creates a new Kubernetes instance with either single or multi-cluster support
func NewKubernetes(config *config.StaticConfig, logger klog.Logger) (*Kubernetes, error) {
	k8s := &Kubernetes{}

	if config.IsMultiClusterEnabled() {
		// Multi-cluster mode
		var err error
		k8s.multiClusterManager, err = NewMultiClusterManager(config, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create multi-cluster manager: %w", err)
		}

		// Start the multi-cluster manager to discover clusters
		ctx := context.Background()
		if err := k8s.multiClusterManager.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start multi-cluster manager: %w", err)
		}
	} else {
		// Single-cluster mode (legacy)
		var err error
		k8s.manager, err = NewManager(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create manager: %w", err)
		}
	}

	return k8s, nil
}

// NewKubernetesWithMultiClusterManager creates a new Kubernetes instance using an existing MultiClusterManager
func NewKubernetesWithMultiClusterManager(mcm *MultiClusterManager) *Kubernetes {
	return &Kubernetes{
		multiClusterManager: mcm,
	}
}

// NewKubernetesWithSimpleMultiClusterManager creates a new Kubernetes instance using an existing SimpleMultiClusterManager
func NewKubernetesWithSimpleMultiClusterManager(mcm *SimpleMultiClusterManager) *Kubernetes {
	return &Kubernetes{
		simpleMultiClusterManager: mcm,
	}
}

// NewKubernetesWithFreshMultiClusterManager creates a new Kubernetes instance using an existing FreshMultiClusterManager
func NewKubernetesWithFreshMultiClusterManager(mcm *FreshMultiClusterManager) *Kubernetes {
	return &Kubernetes{
		freshMultiClusterManager: mcm,
	}
}

// IsMultiCluster returns true if this Kubernetes instance is in multi-cluster mode
func (k *Kubernetes) IsMultiCluster() bool {
	return k.multiClusterManager != nil || k.simpleMultiClusterManager != nil || k.freshMultiClusterManager != nil
}

// GetManager returns the appropriate manager for the current context
func (k *Kubernetes) GetManager() (*Manager, error) {
	if k.freshMultiClusterManager != nil {
		return k.freshMultiClusterManager.GetActiveManager()
	}
	if k.simpleMultiClusterManager != nil {
		return k.simpleMultiClusterManager.GetActiveManager()
	}
	if k.multiClusterManager != nil {
		return k.multiClusterManager.GetActiveManager()
	}
	return k.manager, nil
}

// GetManagerForCluster returns the manager for a specific cluster (multi-cluster mode only)
func (k *Kubernetes) GetManagerForCluster(clusterName string) (*Manager, error) {
	if k.freshMultiClusterManager != nil {
		return k.freshMultiClusterManager.GetManager(clusterName)
	}
	if k.simpleMultiClusterManager != nil {
		return k.simpleMultiClusterManager.GetManager(clusterName)
	}
	if k.multiClusterManager != nil {
		return k.multiClusterManager.GetManager(clusterName)
	}
	return nil, fmt.Errorf("not in multi-cluster mode")
}

// SwitchCluster switches to a different cluster (multi-cluster mode only)
func (k *Kubernetes) SwitchCluster(clusterName string) error {
	if k.freshMultiClusterManager != nil {
		return k.freshMultiClusterManager.SwitchCluster(clusterName)
	}
	if k.simpleMultiClusterManager != nil {
		return k.simpleMultiClusterManager.SwitchCluster(clusterName)
	}
	if k.multiClusterManager != nil {
		return k.multiClusterManager.SwitchCluster(clusterName)
	}
	return fmt.Errorf("not in multi-cluster mode")
}

// GetActiveCluster returns the currently active cluster name (multi-cluster mode only)
func (k *Kubernetes) GetActiveCluster() string {
	if k.freshMultiClusterManager != nil {
		return k.freshMultiClusterManager.GetActiveCluster()
	}
	if k.simpleMultiClusterManager != nil {
		return k.simpleMultiClusterManager.GetActiveCluster()
	}
	if k.multiClusterManager != nil {
		return k.multiClusterManager.GetActiveCluster()
	}
	return ""
}

// ListClusters returns all available clusters (multi-cluster mode only)
func (k *Kubernetes) ListClusters() []ClusterConfig {
	if k.freshMultiClusterManager != nil {
		clusters := k.freshMultiClusterManager.ListClusters()
		if len(clusters) == 0 {
			return nil
		}
		return clusters
	}
	if k.simpleMultiClusterManager != nil {
		clusters := k.simpleMultiClusterManager.ListClusters()
		if len(clusters) == 0 {
			return nil
		}
		return clusters
	}
	if k.multiClusterManager != nil {
		clusters := k.multiClusterManager.ListClusters()
		if len(clusters) == 0 {
			return nil
		}
		return clusters
	}
	return nil
}

// RefreshClusters refreshes the cluster list (multi-cluster mode only)
func (k *Kubernetes) RefreshClusters(ctx context.Context) error {
	if k.freshMultiClusterManager != nil {
		return k.freshMultiClusterManager.RefreshClusters(ctx)
	}
	if k.simpleMultiClusterManager != nil {
		return k.simpleMultiClusterManager.RefreshClusters(ctx)
	}
	if k.multiClusterManager != nil {
		return k.multiClusterManager.RefreshClusters(ctx)
	}
	return fmt.Errorf("not in multi-cluster mode")
}

// StartMultiCluster starts the multi-cluster manager (multi-cluster mode only)
func (k *Kubernetes) StartMultiCluster(ctx context.Context) error {
	if !k.IsMultiCluster() {
		return fmt.Errorf("not in multi-cluster mode")
	}
	return k.multiClusterManager.Start(ctx)
}

// StopMultiCluster stops the multi-cluster manager (multi-cluster mode only)
func (k *Kubernetes) StopMultiCluster() {
	if k.IsMultiCluster() {
		k.multiClusterManager.Stop()
	}
}

// Close properly closes the Kubernetes instance and all its resources
func (k *Kubernetes) Close() {
	if k.IsMultiCluster() {
		k.multiClusterManager.Stop()
	} else if k.manager != nil {
		k.manager.Close()
	}
}

func (m *Manager) WatchKubeConfig(onKubeConfigChange func() error) {
	if m.clientCmdConfig == nil {
		return
	}
	kubeConfigFiles := m.clientCmdConfig.ConfigAccess().GetLoadingPrecedence()
	if len(kubeConfigFiles) == 0 {
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	for _, file := range kubeConfigFiles {
		_ = watcher.Add(file)
	}
	go func() {
		defer watcher.Close()
		for {
			select {
			case <-m.ctx.Done():
				return
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				_ = onKubeConfigChange()
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	if m.CloseWatchKubeConfig != nil {
		_ = m.CloseWatchKubeConfig()
	}
	m.CloseWatchKubeConfig = watcher.Close
}

func (m *Manager) Close() {
	// Check if already closed
	m.closedMu.Lock()
	if m.closed {
		m.closedMu.Unlock()
		return // Already closed, don't run cleanup again
	}
	m.closed = true
	m.closedMu.Unlock()

	// Cancel context to stop all goroutines
	if m.cancel != nil {
		m.cancel()
	}

	// Run all cleanup functions (only once)
	for _, cleanup := range m.cleanupFuncs {
		if err := cleanup(); err != nil {
			// Log error but continue with other cleanups
			klog.Errorf("Error during cleanup: %v", err)
		}
	}

	// Close kubeconfig watcher
	if m.CloseWatchKubeConfig != nil {
		_ = m.CloseWatchKubeConfig()
	}

	// Clear discovery cache to free memory
	if m.discoveryClient != nil {
		m.discoveryClient.Invalidate()
	}
}

// RegisterCleanup registers a cleanup function to be called when the manager is closed
func (m *Manager) RegisterCleanup(cleanup func() error) {
	m.closedMu.Lock()
	defer m.closedMu.Unlock()

	// Don't add cleanup functions if already closed
	if m.closed {
		return
	}

	m.cleanupFuncs = append(m.cleanupFuncs, cleanup)
}

// GetContext returns the manager's context for long-running operations
func (m *Manager) GetContext() context.Context {
	return m.ctx
}

// GetDynamicClient returns the dynamic client
func (m *Manager) GetDynamicClient() *dynamic.DynamicClient {
	return m.dynamicClient
}

func (m *Manager) GetAPIServerHost() string {
	if m.cfg == nil {
		return ""
	}
	return m.cfg.Host
}

func (m *Manager) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return m.discoveryClient, nil
}

func (m *Manager) ToRESTMapper() (meta.RESTMapper, error) {
	return m.accessControlRESTMapper, nil
}

func (m *Manager) Derived(ctx context.Context) (*Kubernetes, error) {
	authorization, ok := ctx.Value(OAuthAuthorizationHeader).(string)
	if !ok || !strings.HasPrefix(authorization, "Bearer ") {
		if m.staticConfig.RequireOAuth {
			return nil, errors.New("oauth token required")
		}
		return &Kubernetes{manager: m}, nil
	}
	klog.V(5).Infof("%s header found (Bearer), using provided bearer token", OAuthAuthorizationHeader)
	derivedCfg := &rest.Config{
		Host:    m.cfg.Host,
		APIPath: m.cfg.APIPath,
		// Copy only server verification TLS settings (CA bundle and server name)
		TLSClientConfig: rest.TLSClientConfig{
			Insecure:   m.cfg.Insecure,
			ServerName: m.cfg.ServerName,
			CAFile:     m.cfg.CAFile,
			CAData:     m.cfg.CAData,
		},
		BearerToken: strings.TrimPrefix(authorization, "Bearer "),
		// pass custom UserAgent to identify the client
		UserAgent:   CustomUserAgent,
		QPS:         m.cfg.QPS,
		Burst:       m.cfg.Burst,
		Timeout:     m.cfg.Timeout,
		Impersonate: rest.ImpersonationConfig{},
	}
	clientCmdApiConfig, err := m.clientCmdConfig.RawConfig()
	if err != nil {
		if m.staticConfig.RequireOAuth {
			klog.Errorf("failed to get kubeconfig: %v", err)
			return nil, errors.New("failed to get kubeconfig")
		}
		return &Kubernetes{manager: m}, nil
	}
	clientCmdApiConfig.AuthInfos = make(map[string]*clientcmdapi.AuthInfo)
	derived := &Kubernetes{manager: &Manager{
		clientCmdConfig: clientcmd.NewDefaultClientConfig(clientCmdApiConfig, nil),
		cfg:             derivedCfg,
		staticConfig:    m.staticConfig,
	}}
	derived.manager.accessControlClientSet, err = NewAccessControlClientset(derived.manager.cfg, derived.manager.staticConfig)
	if err != nil {
		if m.staticConfig.RequireOAuth {
			klog.Errorf("failed to get kubeconfig: %v", err)
			return nil, errors.New("failed to get kubeconfig")
		}
		return &Kubernetes{manager: m}, nil
	}
	derived.manager.discoveryClient = memory.NewMemCacheClient(derived.manager.accessControlClientSet.DiscoveryClient())
	derived.manager.accessControlRESTMapper = NewAccessControlRESTMapper(
		restmapper.NewDeferredDiscoveryRESTMapper(derived.manager.discoveryClient),
		derived.manager.staticConfig,
	)
	derived.manager.dynamicClient, err = dynamic.NewForConfig(derived.manager.cfg)
	if err != nil {
		if m.staticConfig.RequireOAuth {
			klog.Errorf("failed to initialize dynamic client: %v", err)
			return nil, errors.New("failed to initialize dynamic client")
		}
		return &Kubernetes{manager: m}, nil
	}
	return derived, nil
}

func (k *Kubernetes) NewHelm() *helm.Helm {
	// This is a derived Kubernetes, so it already has the Helm initialized
	return helm.NewHelm(k.manager)
}
