package kubernetes

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/clientcmd/api/latest"
	"k8s.io/klog/v2"
)

// InClusterConfig is a variable that holds the function to get the in-cluster config
// Exposed for testing
var InClusterConfig = func() (*rest.Config, error) {
	// TODO use kubernetes.default.svc instead of resolved server
	// Currently running into: `http: server gave HTTP response to HTTPS client`
	inClusterConfig, err := rest.InClusterConfig()
	if inClusterConfig != nil {
		inClusterConfig.Host = "https://kubernetes.default.svc"
	}
	return inClusterConfig, err
}

// resolveKubernetesConfigurations resolves the required kubernetes configurations and sets them in the Kubernetes struct
func resolveKubernetesConfigurations(kubernetes *Manager) error {
	// Always set clientCmdConfig
	// Create fresh loading rules to avoid any caching issues
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubernetes.staticConfig.KubeConfig != "" {
		// Set explicit path and clear any default paths to ensure isolation
		loadingRules.ExplicitPath = kubernetes.staticConfig.KubeConfig
		loadingRules.Precedence = []string{kubernetes.staticConfig.KubeConfig}
	}
	kubernetes.clientCmdConfig = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: ""}})
	var err error
	if kubernetes.IsInCluster() {
		kubernetes.cfg, err = InClusterConfig()
		if err == nil && kubernetes.cfg != nil {
			return nil
		}
	}
	// Out of cluster
	kubernetes.cfg, err = kubernetes.clientCmdConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to load client config from kubeconfig: %w", err)
	}

	// Set user agent if not already set
	if kubernetes.cfg != nil && kubernetes.cfg.UserAgent == "" {
		kubernetes.cfg.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	// Validate and log authentication configuration
	if kubernetes.cfg != nil {
		authMethod := getAuthMethod(kubernetes.cfg)
		klog.Infof("Loaded kubeconfig from %s for server %s with auth method: %s",
			kubernetes.staticConfig.KubeConfig, kubernetes.cfg.Host, authMethod)

		// Log detailed auth info for debugging
		if kubernetes.cfg.BearerToken != "" {
			klog.V(3).Infof("Bearer token present: %d characters", len(kubernetes.cfg.BearerToken))
		}
		if kubernetes.cfg.BearerTokenFile != "" {
			klog.V(3).Infof("Bearer token file: %s", kubernetes.cfg.BearerTokenFile)
		}

		// Warn if no authentication is configured
		if authMethod == "unknown" {
			klog.Warningf("No authentication credentials found in kubeconfig for server %s",
				kubernetes.cfg.Host)
		}
	}

	return nil
}

func (m *Manager) IsInCluster() bool {
	if m.staticConfig.KubeConfig != "" {
		return false
	}
	cfg, err := InClusterConfig()
	return err == nil && cfg != nil
}

func (m *Manager) configuredNamespace() string {
	if ns, _, nsErr := m.clientCmdConfig.Namespace(); nsErr == nil {
		return ns
	}
	return ""
}

func (m *Manager) NamespaceOrDefault(namespace string) string {
	if namespace == "" {
		return m.configuredNamespace()
	}
	return namespace
}

func (k *Kubernetes) NamespaceOrDefault(namespace string) string {
	return k.manager.NamespaceOrDefault(namespace)
}

// ToRESTConfig returns the rest.Config object (genericclioptions.RESTClientGetter)
func (m *Manager) ToRESTConfig() (*rest.Config, error) {
	return m.cfg, nil
}

// ToRawKubeConfigLoader returns the clientcmd.ClientConfig object (genericclioptions.RESTClientGetter)
func (m *Manager) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return m.clientCmdConfig
}

func (m *Manager) ConfigurationView(minify bool) (runtime.Object, error) {
	var cfg clientcmdapi.Config
	var err error
	if m.IsInCluster() {
		cfg = *clientcmdapi.NewConfig()
		cfg.Clusters["cluster"] = &clientcmdapi.Cluster{
			Server:                m.cfg.Host,
			InsecureSkipTLSVerify: m.cfg.Insecure,
		}
		cfg.AuthInfos["user"] = &clientcmdapi.AuthInfo{
			Token: m.cfg.BearerToken,
		}
		cfg.Contexts["context"] = &clientcmdapi.Context{
			Cluster:  "cluster",
			AuthInfo: "user",
		}
		cfg.CurrentContext = "context"
	} else if cfg, err = m.clientCmdConfig.RawConfig(); err != nil {
		return nil, err
	}
	if minify {
		if err = clientcmdapi.MinifyConfig(&cfg); err != nil {
			return nil, err
		}
	}
	//nolint:staticcheck
	if err = clientcmdapi.FlattenConfig(&cfg); err != nil {
		// ignore error
		//return "", err
	}
	return latest.Scheme.ConvertToVersion(&cfg, latest.ExternalVersion)
}

// getAuthMethod determines what authentication method is configured in the REST config
func getAuthMethod(cfg *rest.Config) string {
	if cfg.BearerToken != "" || cfg.BearerTokenFile != "" {
		return "bearer-token"
	}
	if cfg.Username != "" && cfg.Password != "" {
		return "basic-auth"
	}
	if (cfg.CertFile != "" && cfg.KeyFile != "") ||
		(len(cfg.CertData) > 0 && len(cfg.KeyData) > 0) {
		return "client-cert"
	}
	if cfg.ExecProvider != nil {
		return "exec-provider"
	}
	if cfg.AuthProvider != nil {
		return "auth-provider"
	}
	return "unknown"
}
