package kubernetes

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
)

// GetCurrentKubectlContext returns the current kubectl context and its kubeconfig path
func GetCurrentKubectlContext() (contextName string, kubeconfigPath string, err error) {
	// Get kubeconfig path from env or default
	kubeconfigPath = os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("failed to get home directory: %w", err)
		}
		kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
	}

	// Load the kubeconfig
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Get current context
	contextName = config.CurrentContext
	if contextName == "" {
		return "", "", fmt.Errorf("no current context set in kubeconfig")
	}

	return contextName, kubeconfigPath, nil
}

// ExportCurrentContext exports the current kubectl context to a file
func ExportCurrentContext(outputPath string, logger klog.Logger) error {
	contextName, kubeconfigPath, err := GetCurrentKubectlContext()
	if err != nil {
		return fmt.Errorf("failed to get current context: %w", err)
	}

	logger.Info("Exporting kubectl context",
		"context", contextName,
		"from", kubeconfigPath,
		"to", outputPath)

	// Load the full config
	loadingRules := &clientcmd.ClientConfigLoadingRules{
		ExplicitPath: kubeconfigPath,
	}
	config, err := loadingRules.Load()
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create a new config with only the current context
	exportConfig := api.NewConfig()

	// Copy current context
	if context, exists := config.Contexts[contextName]; exists {
		exportConfig.Contexts[contextName] = context
		exportConfig.CurrentContext = contextName

		// Copy referenced cluster
		if cluster, exists := config.Clusters[context.Cluster]; exists {
			exportConfig.Clusters[context.Cluster] = cluster
		}

		// Copy referenced auth info
		if authInfo, exists := config.AuthInfos[context.AuthInfo]; exists {
			exportConfig.AuthInfos[context.AuthInfo] = authInfo
		}
	} else {
		return fmt.Errorf("context %s not found in kubeconfig", contextName)
	}

	// Write to file
	if err := clientcmd.WriteToFile(*exportConfig, outputPath); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	logger.Info("Successfully exported kubectl context",
		"context", contextName,
		"file", outputPath)

	return nil
}

// SyncKubectlContext ensures the current kubectl context is available in the MCP config directory
func SyncKubectlContext(configDir string, logger klog.Logger) (string, error) {
	contextName, _, err := GetCurrentKubectlContext()
	if err != nil {
		return "", err
	}

	// Sanitize context name for filename
	safeContextName := filepath.Base(contextName)
	outputPath := filepath.Join(configDir, fmt.Sprintf("kubectl-%s.yaml", safeContextName))

	// Export the current context
	if err := ExportCurrentContext(outputPath, logger); err != nil {
		return "", err
	}

	return safeContextName, nil
}
