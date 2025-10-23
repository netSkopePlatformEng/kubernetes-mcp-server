package rancher

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a simple REST API client for Rancher
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// Cluster represents a Rancher cluster from the API
type Cluster struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	State   string            `json:"state"`
	Actions map[string]string `json:"actions"`
}

// ClusterListResponse represents the API response for listing clusters
type ClusterListResponse struct {
	Data []Cluster `json:"data"`
}

// GenerateKubeconfigResponse represents the API response for generateKubeconfig action
type GenerateKubeconfigResponse struct {
	Config string `json:"config"`
}

// NewClient creates a new Rancher REST API client
func NewClient(baseURL, token string, insecure bool) *Client {
	// Ensure baseURL ends with /v3
	if !strings.HasSuffix(baseURL, "/v3") {
		baseURL = strings.TrimSuffix(baseURL, "/") + "/v3"
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	if insecure {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // #nosec G402
			},
		}
	}

	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: httpClient,
	}
}

// doRequest performs an HTTP request with authentication
func (c *Client) doRequest(method, path string, body io.Reader) ([]byte, error) {
	url := c.baseURL + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	responseData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(responseData))
	}

	return responseData, nil
}

// ListClusters retrieves all clusters from Rancher
func (c *Client) ListClusters() ([]Cluster, error) {
	data, err := c.doRequest("GET", "/clusters", nil)
	if err != nil {
		return nil, err
	}

	var response ClusterListResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("parsing cluster list: %w", err)
	}

	return response.Data, nil
}

// GetCluster retrieves a specific cluster by name
func (c *Client) GetCluster(clusterName string) (*Cluster, error) {
	clusters, err := c.ListClusters()
	if err != nil {
		return nil, err
	}

	for i := range clusters {
		if clusters[i].Name == clusterName {
			return &clusters[i], nil
		}
	}

	return nil, fmt.Errorf("cluster %q not found", clusterName)
}

// GenerateKubeconfig generates a kubeconfig for the specified cluster
func (c *Client) GenerateKubeconfig(cluster *Cluster) (string, error) {
	// Use the generateKubeconfig action URL from the cluster
	actionURL, ok := cluster.Actions["generateKubeconfig"]
	if !ok {
		return "", fmt.Errorf("cluster %q does not have generateKubeconfig action", cluster.Name)
	}

	// Extract the path from the full URL
	// Example: "https://rancher.../v3/clusters/c-26wzq?action=generateKubeconfig"
	// We want: "/clusters/c-26wzq?action=generateKubeconfig"
	path := strings.TrimPrefix(actionURL, c.baseURL)

	data, err := c.doRequest("POST", path, nil)
	if err != nil {
		return "", err
	}

	var response GenerateKubeconfigResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("parsing kubeconfig response: %w", err)
	}

	return response.Config, nil
}

// GenerateKubeconfigByName generates a kubeconfig for a cluster by name
func (c *Client) GenerateKubeconfigByName(clusterName string) (string, error) {
	cluster, err := c.GetCluster(clusterName)
	if err != nil {
		return "", err
	}

	return c.GenerateKubeconfig(cluster)
}
