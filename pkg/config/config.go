package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// StaticConfig is the configuration for the server.
// It allows to configure server specific settings and tools to be enabled or disabled.
type StaticConfig struct {
	DeniedResources []GroupVersionKind `toml:"denied_resources"`

	LogLevel   int    `toml:"log_level,omitempty"`
	Port       string `toml:"port,omitempty"`
	SSEBaseURL string `toml:"sse_base_url,omitempty"`
	// Legacy single kubeconfig support (deprecated in favor of multi-cluster)
	KubeConfig string `toml:"kubeconfig,omitempty"`
	
	// Multi-cluster configuration
	KubeConfigDir     string            `toml:"kubeconfig_dir,omitempty"`
	DefaultCluster    string            `toml:"default_cluster,omitempty"`
	AutoDiscovery     bool              `toml:"auto_discovery,omitempty"`
	ClusterAliases    map[string]string `toml:"cluster_aliases,omitempty"`
	
	// NSK Integration
	NSKIntegration    *NSKConfig        `toml:"nsk,omitempty"`
	
	ListOutput string `toml:"list_output,omitempty"`
	// When true, expose only tools annotated with readOnlyHint=true
	ReadOnly bool `toml:"read_only,omitempty"`
	// When true, disable tools annotated with destructiveHint=true
	DisableDestructive bool     `toml:"disable_destructive,omitempty"`
	EnabledTools       []string `toml:"enabled_tools,omitempty"`
	DisabledTools      []string `toml:"disabled_tools,omitempty"`

	// Authorization-related fields
	// RequireOAuth indicates whether the server requires OAuth for authentication.
	RequireOAuth bool `toml:"require_oauth,omitempty"`
	// OAuthAudience is the valid audience for the OAuth tokens, used for offline JWT claim validation.
	OAuthAudience string `toml:"oauth_audience,omitempty"`
	// ValidateToken indicates whether the server should validate the token against the Kubernetes API Server using TokenReview.
	ValidateToken bool `toml:"validate_token,omitempty"`
	// AuthorizationURL is the URL of the OIDC authorization server.
	// It is used for token validation and for STS token exchange.
	AuthorizationURL string `toml:"authorization_url,omitempty"`
	// DisableDynamicClientRegistration indicates whether dynamic client registration is disabled.
	// If true, the .well-known endpoints will not expose the registration endpoint.
	DisableDynamicClientRegistration bool `toml:"disable_dynamic_client_registration,omitempty"`
	// OAuthScopes are the supported **client** scopes requested during the **client/frontend** OAuth flow.
	OAuthScopes []string `toml:"oauth_scopes,omitempty"`
	// StsClientId is the OAuth client ID used for backend token exchange
	StsClientId string `toml:"sts_client_id,omitempty"`
	// StsClientSecret is the OAuth client secret used for backend token exchange
	StsClientSecret string `toml:"sts_client_secret,omitempty"`
	// StsAudience is the audience for the STS token exchange.
	StsAudience string `toml:"sts_audience,omitempty"`
	// StsScopes is the scopes for the STS token exchange.
	StsScopes            []string `toml:"sts_scopes,omitempty"`
	CertificateAuthority string   `toml:"certificate_authority,omitempty"`
	ServerURL            string   `toml:"server_url,omitempty"`
}

type GroupVersionKind struct {
	Group   string `toml:"group"`
	Version string `toml:"version"`
	Kind    string `toml:"kind,omitempty"`
}

// NSKConfig represents configuration for NSK (Netskope Kubernetes) tool integration
type NSKConfig struct {
	// Core NSK settings
	Enabled           bool     `toml:"enabled,omitempty"`
	NSKPath           string   `toml:"nsk_path,omitempty"`           // Path to NSK binary, defaults to "nsk"
	
	// Rancher environment settings
	RancherURL        string   `toml:"rancher_url,omitempty"`
	RancherToken      string   `toml:"rancher_token,omitempty"`
	Profile           string   `toml:"profile,omitempty"`
	ConfigDir         string   `toml:"config_dir,omitempty"`
	
	// Auto-refresh settings
	AutoRefresh       bool     `toml:"auto_refresh,omitempty"`
	RefreshInterval   string   `toml:"refresh_interval,omitempty"`  // e.g., "1h", "30m"
	
	// Cluster filtering
	ClusterPattern    string   `toml:"cluster_pattern,omitempty"`   // Regex pattern for cluster names
	ExcludeClusters   []string `toml:"exclude_clusters,omitempty"`
	IncludeClusters   []string `toml:"include_clusters,omitempty"`
	
	// Environment variables for NSK execution
	Environment       map[string]string `toml:"environment,omitempty"`
}

// ReadConfig reads the toml file and returns the StaticConfig.
func ReadConfig(configPath string) (*StaticConfig, error) {
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config *StaticConfig
	err = toml.Unmarshal(configData, &config)
	if err != nil {
		return nil, err
	}
	return config, nil
}

// ValidateMultiClusterConfig validates the multi-cluster configuration
func (c *StaticConfig) ValidateMultiClusterConfig() error {
	if c.KubeConfigDir == "" && c.KubeConfig == "" {
		return fmt.Errorf("either kubeconfig_dir or kubeconfig must be specified")
	}
	
	if c.KubeConfigDir != "" && c.KubeConfig != "" {
		return fmt.Errorf("cannot specify both kubeconfig_dir and kubeconfig")
	}
	
	return nil
}

// IsMultiClusterEnabled returns true if multi-cluster mode is enabled
func (c *StaticConfig) IsMultiClusterEnabled() bool {
	return c.KubeConfigDir != ""
}

// IsNSKEnabled returns true if NSK integration is enabled
func (c *StaticConfig) IsNSKEnabled() bool {
	return c.NSKIntegration != nil && c.NSKIntegration.Enabled
}
