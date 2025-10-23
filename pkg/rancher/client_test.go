package rancher

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		expectedURL string
	}{
		{
			name:        "URL without /v3",
			baseURL:     "https://rancher.example.com",
			expectedURL: "https://rancher.example.com/v3",
		},
		{
			name:        "URL with /v3",
			baseURL:     "https://rancher.example.com/v3",
			expectedURL: "https://rancher.example.com/v3",
		},
		{
			name:        "URL with trailing slash",
			baseURL:     "https://rancher.example.com/",
			expectedURL: "https://rancher.example.com/v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.baseURL, "test-token", true)
			if client.baseURL != tt.expectedURL {
				t.Errorf("NewClient() baseURL = %v, want %v", client.baseURL, tt.expectedURL)
			}
		})
	}
}
