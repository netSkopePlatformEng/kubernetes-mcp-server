package mcp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	authenticationapiv1 "k8s.io/api/authentication/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	internalk8s "github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/mcp/jobs"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/output"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/version"
)

type ContextKey string

const TokenScopesContextKey = ContextKey("TokenScopesContextKey")

type Configuration struct {
	Profile    Profile
	ListOutput output.Output

	StaticConfig *config.StaticConfig
}

func (c *Configuration) isToolApplicable(tool server.ServerTool) bool {
	if c.StaticConfig.ReadOnly && !ptr.Deref(tool.Tool.Annotations.ReadOnlyHint, false) {
		return false
	}
	if c.StaticConfig.DisableDestructive && ptr.Deref(tool.Tool.Annotations.DestructiveHint, false) {
		return false
	}
	if c.StaticConfig.EnabledTools != nil && !slices.Contains(c.StaticConfig.EnabledTools, tool.Tool.Name) {
		return false
	}
	if c.StaticConfig.DisabledTools != nil && slices.Contains(c.StaticConfig.DisabledTools, tool.Tool.Name) {
		return false
	}
	return true
}

type Server struct {
	configuration  *Configuration
	server         *server.MCPServer
	enabledTools   []string
	k              *internalk8s.Manager
	k8s            *internalk8s.Kubernetes               // Add persistent Kubernetes instance for multi-cluster
	clusterManager *internalk8s.FreshMultiClusterManager // Fresh multi-cluster manager (kubectl-style)
	rancher        *internalk8s.RancherIntegration       // Rancher integration for kubeconfig management
	jobManager     *jobs.Manager                         // Job manager for async long-running tasks
}

func NewServer(configuration Configuration) (*Server, error) {
	var serverOptions []server.ServerOption
	serverOptions = append(serverOptions,
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
		server.WithToolCapabilities(true),
		server.WithLogging(),
		server.WithToolHandlerMiddleware(toolCallLoggingMiddleware),
	)
	if configuration.StaticConfig.RequireOAuth && false { // TODO: Disabled scope auth validation for now
		serverOptions = append(serverOptions, server.WithToolHandlerMiddleware(toolScopedAuthorizationMiddleware))
	}

	s := &Server{
		configuration: &configuration,
		server: server.NewMCPServer(
			version.BinaryName,
			version.Version,
			serverOptions...,
		),
	}

	// Initialize multi-cluster support if enabled
	if configuration.StaticConfig.IsMultiClusterEnabled() {
		if err := s.initializeMultiCluster(); err != nil {
			return nil, fmt.Errorf("failed to initialize multi-cluster support: %w", err)
		}
	}

	// Initialize JobManager for async long-running tasks
	if err := s.initializeJobManager(); err != nil {
		return nil, fmt.Errorf("failed to initialize job manager: %w", err)
	}

	if err := s.reloadKubernetesClient(); err != nil {
		return nil, err
	}
	// Only watch kubeconfig if we have an active manager
	if s.k != nil {
		s.k.WatchKubeConfig(s.reloadKubernetesClient)
	}

	return s, nil
}

// initializeMultiCluster initializes multi-cluster support components
func (s *Server) initializeMultiCluster() error {
	logger := klog.Background()

	// Initialize fresh multi-cluster manager (kubectl-style - creates fresh clients per operation)
	mcm, err := internalk8s.NewFreshMultiClusterManager(s.configuration.StaticConfig, logger)
	if err != nil {
		return fmt.Errorf("failed to create fresh multi-cluster manager: %w", err)
	}
	s.clusterManager = mcm

	// Initialize Rancher integration if configured
	if s.configuration.StaticConfig.IsRancherEnabled() {
		s.rancher = internalk8s.NewRancherIntegration(
			s.configuration.StaticConfig.RancherIntegration,
			s.clusterManager,
			logger,
		)
		logger.Info("Rancher integration initialized",
			"url", s.configuration.StaticConfig.RancherIntegration.URL,
			"config_dir", s.configuration.StaticConfig.RancherIntegration.ConfigDir)
	}

	// Start the multi-cluster manager
	ctx := context.Background()
	if err := s.clusterManager.Start(ctx); err != nil {
		logger.Error(err, "Failed to start multi-cluster manager")
		// Don't fail if no clusters found initially
	}

	logger.Info("Multi-cluster support initialized",
		"clusters", len(s.clusterManager.ListClusters()),
		"rancher_enabled", s.configuration.StaticConfig.IsRancherEnabled())

	return nil
}

// initializeJobManager initializes the job manager for async long-running tasks
func (s *Server) initializeJobManager() error {
	logger := klog.Background()

	// Create job manager with default configuration
	config := jobs.DefaultManagerConfig()
	// Override with custom settings if needed
	config.StorageDir = "~/.mcp/jobs"
	config.MaxJobs = 100
	config.WorkerCount = 5

	jobManager, err := jobs.NewManager(config)
	if err != nil {
		return fmt.Errorf("failed to create job manager: %w", err)
	}

	s.jobManager = jobManager
	logger.Info("JobManager initialized", "workers", config.WorkerCount, "maxJobs", config.MaxJobs)

	return nil
}

func (s *Server) reloadKubernetesClient() error {
	logger := klog.Background()

	// Check if multi-cluster mode is enabled and use appropriate initialization
	if s.configuration.StaticConfig.IsMultiClusterEnabled() {
		// Create or reuse the multi-cluster Kubernetes instance
		if s.k8s == nil {
			logger.V(2).Info("Creating new multi-cluster Kubernetes instance")
			// Use the existing clusterManager if available, otherwise create a new one
			if s.clusterManager != nil {
				// Use existing cluster manager to maintain cluster switch state
				s.k8s = internalk8s.NewKubernetesWithFreshMultiClusterManager(s.clusterManager)
				logger.V(2).Info("Created Kubernetes instance with existing fresh cluster manager")
			} else {
				// Create new instance (should not happen in multi-cluster mode)
				k8s, err := internalk8s.NewKubernetes(s.configuration.StaticConfig, logger)
				if err != nil {
					logger.Error(err, "Failed to create multi-cluster Kubernetes instance")
					return err
				}
				s.k8s = k8s
			}
		}

		// Get the Manager from the Kubernetes instance (which reflects current active cluster)
		activeCluster := s.k8s.GetActiveCluster()

		// If there are no clusters yet (empty kubeconfig directory), skip manager initialization
		// Tools will fail gracefully when called if no cluster is active
		if activeCluster == "" {
			logger.V(2).Info("No clusters available yet, skipping manager initialization")
			s.k = nil
		} else {
			logger.V(2).Info("Getting manager for active cluster", "cluster", activeCluster)

			manager, err := s.k8s.GetManager()
			if err != nil {
				logger.Error(err, "Failed to get manager for active cluster", "cluster", activeCluster)
				return err
			}

			// Validate the manager has proper configuration
			restConfig, err := manager.ToRESTConfig()
			if err != nil || restConfig == nil {
				return fmt.Errorf("manager for cluster %s has no valid rest config: %w", activeCluster, err)
			}

			logger.V(2).Info("Successfully reloaded manager", "cluster", activeCluster, "server", restConfig.Host)
			s.k = manager
		}
	} else {
		// Use legacy single-cluster manager
		logger.V(2).Info("Creating single-cluster manager")
		k, err := internalk8s.NewManager(s.configuration.StaticConfig)
		if err != nil {
			logger.Error(err, "Failed to create single-cluster manager")
			return err
		}
		s.k = k
	}
	applicableTools := make([]server.ServerTool, 0)
	for _, tool := range s.configuration.Profile.GetTools(s) {
		if !s.configuration.isToolApplicable(tool) {
			continue
		}
		applicableTools = append(applicableTools, tool)
		s.enabledTools = append(s.enabledTools, tool.Tool.Name)
	}
	// Only set tools if server is initialized (not nil in tests)
	if s.server != nil {
		s.server.SetTools(applicableTools...)
	}
	return nil
}

// validateManagerAuthentication performs a basic authentication check for the manager
func (s *Server) validateManagerAuthentication(manager *internalk8s.Manager, clusterName string) error {
	logger := klog.Background()

	// Try to get a discovery client and perform a simple API call
	discoveryClient, err := manager.ToDiscoveryClient()
	if err != nil {
		logger.V(3).Info("Failed to get discovery client", "cluster", clusterName, "error", err)
		return fmt.Errorf("failed to get discovery client: %w", err)
	}

	// Perform a basic server version check to validate authentication
	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		logger.V(3).Info("Server version check failed", "cluster", clusterName, "error", err)
		return fmt.Errorf("server version check failed: %w", err)
	}

	logger.V(3).Info("Authentication validation successful", "cluster", clusterName,
		"server_version", serverVersion.String())

	return nil
}

func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.server)
}

func (s *Server) ServeSse(baseUrl string, httpServer *http.Server) *server.SSEServer {
	options := make([]server.SSEOption, 0)
	options = append(options, server.WithSSEContextFunc(contextFunc), server.WithHTTPServer(httpServer))
	if baseUrl != "" {
		options = append(options, server.WithBaseURL(baseUrl))
	}
	return server.NewSSEServer(s.server, options...)
}

func (s *Server) ServeHTTP(httpServer *http.Server) *server.StreamableHTTPServer {
	options := []server.StreamableHTTPOption{
		server.WithHTTPContextFunc(contextFunc),
		server.WithStreamableHTTPServer(httpServer),
		server.WithStateLess(true),
	}
	return server.NewStreamableHTTPServer(s.server, options...)
}

// KubernetesApiVerifyToken verifies the given token with the audience by
// sending an TokenReview request to API Server.
func (s *Server) KubernetesApiVerifyToken(ctx context.Context, token string, audience string) (*authenticationapiv1.UserInfo, []string, error) {
	if s.k == nil {
		return nil, nil, fmt.Errorf("kubernetes manager is not initialized")
	}
	return s.k.VerifyToken(ctx, token, audience)
}

// GetKubernetesAPIServerHost returns the Kubernetes API server host from the configuration.
func (s *Server) GetKubernetesAPIServerHost() string {
	if s.k == nil {
		return ""
	}
	return s.k.GetAPIServerHost()
}

func (s *Server) GetEnabledTools() []string {
	return s.enabledTools
}

func (s *Server) Close() {
	if s.k8s != nil {
		s.k8s.Close()
	} else if s.k != nil {
		s.k.Close()
	}
}

// getFreshDerived gets a fresh derived Kubernetes client for the active cluster.
// In multi-cluster mode, this ensures we always use the correct cluster's manager.
// The cleanup function MUST be called when done to avoid resource leaks.
func (s *Server) getFreshDerived(ctx context.Context) (*internalk8s.Kubernetes, func(), error) {
	// Multi-cluster mode: get fresh manager for active cluster
	if s.k8s != nil {
		manager, err := s.k8s.GetManager()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get manager for active cluster: %w", err)
		}

		derived, err := manager.Derived(ctx)
		if err != nil {
			manager.Close()
			return nil, nil, fmt.Errorf("failed to get derived client: %w", err)
		}

		// Return cleanup function that closes the manager
		cleanup := func() {
			manager.Close()
		}

		return derived, cleanup, nil
	}

	// Single-cluster mode: use cached manager (no cleanup needed)
	if s.k == nil {
		return nil, nil, fmt.Errorf("kubernetes manager is not initialized")
	}

	derived, err := s.k.Derived(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get derived client: %w", err)
	}

	// No cleanup needed for cached manager
	return derived, func() {}, nil
}

func NewTextResult(content string, err error) *mcp.CallToolResult {
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: err.Error(),
				},
			},
		}
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: content,
			},
		},
	}
}

func contextFunc(ctx context.Context, r *http.Request) context.Context {
	// Get the standard Authorization header (OAuth compliant)
	authHeader := r.Header.Get(string(internalk8s.OAuthAuthorizationHeader))
	if authHeader != "" {
		return context.WithValue(ctx, internalk8s.OAuthAuthorizationHeader, authHeader)
	}

	// Fallback to custom header for backward compatibility
	customAuthHeader := r.Header.Get(string(internalk8s.CustomAuthorizationHeader))
	if customAuthHeader != "" {
		return context.WithValue(ctx, internalk8s.OAuthAuthorizationHeader, customAuthHeader)
	}

	return ctx
}

func toolCallLoggingMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, ctr mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		klog.V(5).Infof("mcp tool call: %s(%v)", ctr.Params.Name, ctr.Params.Arguments)
		if ctr.Header != nil {
			buffer := bytes.NewBuffer(make([]byte, 0))
			if err := ctr.Header.WriteSubset(buffer, map[string]bool{"Authorization": true, "authorization": true}); err == nil {
				klog.V(7).Infof("mcp tool call headers: %s", buffer)
			}
		}
		return next(ctx, ctr)
	}
}

func toolScopedAuthorizationMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, ctr mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		scopes, ok := ctx.Value(TokenScopesContextKey).([]string)
		if !ok {
			return NewTextResult("", fmt.Errorf("authorization failed: Access denied: Tool '%s' requires scope 'mcp:%s' but no scope is available", ctr.Params.Name, ctr.Params.Name)), nil
		}
		if !slices.Contains(scopes, "mcp:"+ctr.Params.Name) && !slices.Contains(scopes, ctr.Params.Name) {
			return NewTextResult("", fmt.Errorf("authorization failed: Access denied: Tool '%s' requires scope 'mcp:%s' but only scopes %s are available", ctr.Params.Name, ctr.Params.Name, scopes)), nil
		}
		return next(ctx, ctr)
	}
}
