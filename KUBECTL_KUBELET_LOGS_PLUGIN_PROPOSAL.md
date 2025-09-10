# kubectl-kubelet-logs Plugin Proposal

## Executive Summary

This proposal outlines the development of a `kubectl-kubelet-logs` plugin that enables AI agents and operators to retrieve kubelet logs directly through the Kubernetes API, providing simultaneous access to kubelet diagnostics and API state for comprehensive troubleshooting. The plugin integrates seamlessly with the existing Kubernetes MCP server architecture and provides enhanced debugging capabilities for node-level issues.

**Target Kubernetes Version**: v1.24.17 (all components and dependencies must be compatible with this version)

## Problem Statement

### Current Challenges
- **Limited Node Visibility**: Standard kubectl commands don't provide direct access to kubelet logs
- **Fragmented Debugging**: Requires separate SSH access or node access to view kubelet logs
- **Audit Gap**: No centralized way to access kubelet logs through Kubernetes RBAC
- **AI Agent Limitations**: AI agents cannot correlate API events with kubelet behavior
- **Operational Complexity**: Multiple tools and access methods required for node debugging

### Use Cases
1. **Pod Scheduling Issues**: Correlate kubelet logs with pod events and node conditions
2. **Container Runtime Problems**: Debug container creation, image pull, and runtime errors
3. **Node Health Monitoring**: Monitor kubelet health, readiness, and startup issues
4. **Network Troubleshooting**: Debug CNI plugin issues and network setup problems
5. **Storage Issues**: Investigate volume mounting and storage plugin problems
6. **Resource Management**: Analyze eviction decisions and resource enforcement

## Solution Overview

### kubectl-kubelet-logs Plugin
A comprehensive kubectl plugin that provides secure, RBAC-controlled access to kubelet logs through the Kubernetes API proxy mechanism, enabling AI agents to perform holistic node and cluster debugging.

### Key Features
- **API-Based Access**: Retrieve kubelet logs through Kubernetes API without direct node access
- **RBAC Integration**: Respect existing Kubernetes permissions and audit trails
- **Multiple Output Formats**: Support for streaming, historical, and structured log output
- **Node Discovery**: Automatic node discovery and selection capabilities
- **Correlation Tools**: Built-in correlation with pod events, node conditions, and cluster state
- **MCP Integration**: Native integration with Kubernetes MCP server for AI agent access

## Technical Architecture

### 1. Plugin Core Structure

```go
// cmd/kubectl-kubelet-logs/main.go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/spf13/cobra"
    "k8s.io/cli-runtime/pkg/genericclioptions"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

type KubeletLogsOptions struct {
    configFlags *genericclioptions.ConfigFlags
    genericclioptions.IOStreams

    // Core options
    NodeName      string
    Namespace     string
    Follow        bool
    Previous      bool
    Timestamps    bool
    TailLines     int64
    SinceTime     string
    SinceSeconds  int64

    // Advanced options
    Container     string
    AllNodes      bool
    Selector      string
    Output        string
    
    // Correlation options
    CorrelateWithPods   bool
    CorrelateWithEvents bool
    ShowNodeConditions  bool
    
    // Internal
    clientset     *kubernetes.Clientset
    restConfig    *rest.Config
}

func NewKubeletLogsCommand() *cobra.Command {
    o := &KubeletLogsOptions{
        configFlags: genericclioptions.NewConfigFlags(true),
        IOStreams:   genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr},
    }

    cmd := &cobra.Command{
        Use:   "kubelet-logs [NODE_NAME]",
        Short: "Retrieve kubelet logs from Kubernetes nodes",
        Long: `Retrieve kubelet logs from one or more Kubernetes nodes through the API server.
        
This plugin provides secure access to kubelet logs without requiring direct node access,
enabling comprehensive debugging of node-level issues while respecting RBAC permissions.`,
        Example: `  # Get kubelet logs from a specific node
  kubectl kubelet-logs node-1

  # Follow kubelet logs from all nodes
  kubectl kubelet-logs --all-nodes --follow

  # Get kubelet logs with pod correlation
  kubectl kubelet-logs node-1 --correlate-with-pods

  # Get recent kubelet logs with timestamps
  kubectl kubelet-logs node-1 --timestamps --since=1h

  # Get kubelet logs in JSON format
  kubectl kubelet-logs node-1 --output json`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if err := o.Complete(cmd, args); err != nil {
                return err
            }
            if err := o.Validate(); err != nil {
                return err
            }
            return o.Run(cmd.Context())
        },
    }

    // Add flags
    o.configFlags.AddFlags(cmd.Flags())
    o.addFlags(cmd)
    
    return cmd
}

func (o *KubeletLogsOptions) addFlags(cmd *cobra.Command) {
    cmd.Flags().BoolVarP(&o.Follow, "follow", "f", false, "Specify if the logs should be streamed")
    cmd.Flags().BoolVarP(&o.Previous, "previous", "p", false, "If true, print the logs for the previous instance of the kubelet")
    cmd.Flags().BoolVar(&o.Timestamps, "timestamps", false, "Include timestamps on each line in the log output")
    cmd.Flags().Int64Var(&o.TailLines, "tail", -1, "Lines of recent log file to display. Defaults to -1 with no selector")
    cmd.Flags().StringVar(&o.SinceTime, "since-time", "", "Only return logs after a specific date (RFC3339)")
    cmd.Flags().Int64Var(&o.SinceSeconds, "since", 0, "Only return logs newer than a relative duration like 5s, 2m, or 3h")
    
    cmd.Flags().BoolVar(&o.AllNodes, "all-nodes", false, "Get kubelet logs from all nodes")
    cmd.Flags().StringVarP(&o.Selector, "selector", "l", "", "Selector (label query) to filter nodes")
    cmd.Flags().StringVarP(&o.Output, "output", "o", "text", "Output format. One of: text|json|yaml")
    
    cmd.Flags().BoolVar(&o.CorrelateWithPods, "correlate-with-pods", false, "Include pod information running on the node")
    cmd.Flags().BoolVar(&o.CorrelateWithEvents, "correlate-with-events", false, "Include recent events related to the node")
    cmd.Flags().BoolVar(&o.ShowNodeConditions, "show-node-conditions", false, "Include current node conditions")
}
```

### 2. Kubelet Log Retrieval Engine

```go
// pkg/kubeletlogs/retriever.go
package kubeletlogs

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"
    "time"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

type LogRetriever struct {
    clientset  *kubernetes.Clientset
    restConfig *rest.Config
}

type LogRequest struct {
    NodeName        string
    Follow          bool
    Previous        bool
    Timestamps      bool
    TailLines       *int64
    SinceTime       *metav1.Time
    SinceSeconds    *int64
    LimitBytes      *int64
}

type LogResponse struct {
    NodeName    string      `json:"nodeName"`
    Timestamp   time.Time   `json:"timestamp"`
    Logs        string      `json:"logs"`
    Error       string      `json:"error,omitempty"`
    Metadata    LogMetadata `json:"metadata,omitempty"`
}

type LogMetadata struct {
    KubeletVersion   string                 `json:"kubeletVersion,omitempty"`
    NodeConditions   []corev1.NodeCondition `json:"nodeConditions,omitempty"`
    RunningPods      []PodInfo              `json:"runningPods,omitempty"`
    RecentEvents     []EventInfo            `json:"recentEvents,omitempty"`
}

type PodInfo struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
    Phase     string `json:"phase"`
    Ready     bool   `json:"ready"`
}

type EventInfo struct {
    Type      string    `json:"type"`
    Reason    string    `json:"reason"`
    Message   string    `json:"message"`
    Timestamp time.Time `json:"timestamp"`
}

func NewLogRetriever(clientset *kubernetes.Clientset, restConfig *rest.Config) *LogRetriever {
    return &LogRetriever{
        clientset:  clientset,
        restConfig: restConfig,
    }
}

func (lr *LogRetriever) GetKubeletLogs(ctx context.Context, req LogRequest) (*LogResponse, error) {
    // Validate node exists
    node, err := lr.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to get node %s: %w", req.NodeName, err)
    }

    // Build kubelet logs URL
    logsURL := lr.buildKubeletLogsURL(req)
    
    // Create HTTP request through API server proxy
    request := lr.clientset.CoreV1().RESTClient().Get().
        Resource("nodes").
        Name(req.NodeName).
        SubResource("proxy").
        Suffix("logs/kubelet.log")
    
    // Add query parameters
    request = lr.addQueryParams(request, req)
    
    // Execute request
    stream, err := request.Stream(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to get kubelet logs: %w", err)
    }
    defer stream.Close()

    // Read logs
    logs, err := io.ReadAll(stream)
    if err != nil {
        return nil, fmt.Errorf("failed to read kubelet logs: %w", err)
    }

    // Build response
    response := &LogResponse{
        NodeName:  req.NodeName,
        Timestamp: time.Now(),
        Logs:      string(logs),
    }

    // Add metadata if requested
    if metadata, err := lr.buildMetadata(ctx, node); err == nil {
        response.Metadata = *metadata
    }

    return response, nil
}

func (lr *LogRetriever) buildKubeletLogsURL(req LogRequest) string {
    params := url.Values{}
    
    if req.Follow {
        params.Add("follow", "true")
    }
    if req.Previous {
        params.Add("previous", "true")
    }
    if req.Timestamps {
        params.Add("timestamps", "true")
    }
    if req.TailLines != nil {
        params.Add("tailLines", fmt.Sprintf("%d", *req.TailLines))
    }
    if req.SinceTime != nil {
        params.Add("sinceTime", req.SinceTime.Format(time.RFC3339))
    }
    if req.SinceSeconds != nil {
        params.Add("sinceSeconds", fmt.Sprintf("%d", *req.SinceSeconds))
    }
    
    baseURL := "/logs/kubelet.log"
    if len(params) > 0 {
        baseURL += "?" + params.Encode()
    }
    
    return baseURL
}

func (lr *LogRetriever) buildMetadata(ctx context.Context, node *corev1.Node) (*LogMetadata, error) {
    metadata := &LogMetadata{
        KubeletVersion: node.Status.NodeInfo.KubeletVersion,
        NodeConditions: node.Status.Conditions,
    }

    // Get running pods on the node
    pods, err := lr.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
        FieldSelector: fmt.Sprintf("spec.nodeName=%s", node.Name),
    })
    if err == nil {
        for _, pod := range pods.Items {
            podInfo := PodInfo{
                Name:      pod.Name,
                Namespace: pod.Namespace,
                Phase:     string(pod.Status.Phase),
                Ready:     isPodReady(&pod),
            }
            metadata.RunningPods = append(metadata.RunningPods, podInfo)
        }
    }

    // Get recent events for the node
    events, err := lr.clientset.CoreV1().Events("").List(ctx, metav1.ListOptions{
        FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Node", node.Name),
        Limit:         10,
    })
    if err == nil {
        for _, event := range events.Items {
            eventInfo := EventInfo{
                Type:      event.Type,
                Reason:    event.Reason,
                Message:   event.Message,
                Timestamp: event.FirstTimestamp.Time,
            }
            metadata.RecentEvents = append(metadata.RecentEvents, eventInfo)
        }
    }

    return metadata, nil
}

func isPodReady(pod *corev1.Pod) bool {
    for _, condition := range pod.Status.Conditions {
        if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
            return true
        }
    }
    return false
}
```

### 3. Multi-Node Support

```go
// pkg/kubeletlogs/multinode.go
package kubeletlogs

import (
    "context"
    "fmt"
    "sync"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
)

type MultiNodeRetriever struct {
    logRetriever *LogRetriever
    clientset    *kubernetes.Clientset
}

type MultiNodeRequest struct {
    AllNodes         bool
    NodeSelector     string
    NodeNames        []string
    LogRequest       LogRequest
    MaxConcurrency   int
}

type MultiNodeResponse struct {
    Responses []LogResponse `json:"responses"`
    Errors    []NodeError   `json:"errors,omitempty"`
    Summary   Summary       `json:"summary"`
}

type NodeError struct {
    NodeName string `json:"nodeName"`
    Error    string `json:"error"`
}

type Summary struct {
    TotalNodes    int `json:"totalNodes"`
    SuccessNodes  int `json:"successNodes"`
    ErrorNodes    int `json:"errorNodes"`
    ExecutionTime string `json:"executionTime"`
}

func NewMultiNodeRetriever(logRetriever *LogRetriever, clientset *kubernetes.Clientset) *MultiNodeRetriever {
    return &MultiNodeRetriever{
        logRetriever: logRetriever,
        clientset:    clientset,
    }
}

func (mnr *MultiNodeRetriever) GetKubeletLogsFromMultipleNodes(ctx context.Context, req MultiNodeRequest) (*MultiNodeResponse, error) {
    startTime := time.Now()
    
    // Determine target nodes
    targetNodes, err := mnr.getTargetNodes(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("failed to determine target nodes: %w", err)
    }

    if len(targetNodes) == 0 {
        return &MultiNodeResponse{
            Summary: Summary{
                TotalNodes:    0,
                SuccessNodes:  0,
                ErrorNodes:    0,
                ExecutionTime: time.Since(startTime).String(),
            },
        }, nil
    }

    // Set up concurrency control
    maxConcurrency := req.MaxConcurrency
    if maxConcurrency <= 0 {
        maxConcurrency = 5 // Default concurrency
    }
    
    semaphore := make(chan struct{}, maxConcurrency)
    var wg sync.WaitGroup
    var mutex sync.Mutex
    
    var responses []LogResponse
    var errors []NodeError

    // Process nodes concurrently
    for _, nodeName := range targetNodes {
        wg.Add(1)
        go func(node string) {
            defer wg.Done()
            
            // Acquire semaphore
            semaphore <- struct{}{}
            defer func() { <-semaphore }()

            // Prepare request for this node
            nodeReq := req.LogRequest
            nodeReq.NodeName = node

            // Get logs for this node
            response, err := mnr.logRetriever.GetKubeletLogs(ctx, nodeReq)
            
            mutex.Lock()
            if err != nil {
                errors = append(errors, NodeError{
                    NodeName: node,
                    Error:    err.Error(),
                })
            } else {
                responses = append(responses, *response)
            }
            mutex.Unlock()
        }(nodeName)
    }

    wg.Wait()

    return &MultiNodeResponse{
        Responses: responses,
        Errors:    errors,
        Summary: Summary{
            TotalNodes:    len(targetNodes),
            SuccessNodes:  len(responses),
            ErrorNodes:    len(errors),
            ExecutionTime: time.Since(startTime).String(),
        },
    }, nil
}

func (mnr *MultiNodeRetriever) getTargetNodes(ctx context.Context, req MultiNodeRequest) ([]string, error) {
    if len(req.NodeNames) > 0 {
        return req.NodeNames, nil
    }

    listOptions := metav1.ListOptions{}
    if req.NodeSelector != "" {
        listOptions.LabelSelector = req.NodeSelector
    }

    nodes, err := mnr.clientset.CoreV1().Nodes().List(ctx, listOptions)
    if err != nil {
        return nil, err
    }

    var nodeNames []string
    for _, node := range nodes.Items {
        nodeNames = append(nodeNames, node.Name)
    }

    return nodeNames, nil
}
```

### 4. Output Formatters

```go
// pkg/kubeletlogs/formatters.go
package kubeletlogs

import (
    "encoding/json"
    "fmt"
    "io"
    "strings"
    "time"

    "gopkg.in/yaml.v2"
)

type OutputFormatter interface {
    Format(response interface{}) ([]byte, error)
    FormatStream(response interface{}, writer io.Writer) error
}

type TextFormatter struct {
    ShowMetadata bool
    Timestamps   bool
}

type JSONFormatter struct {
    Pretty bool
}

type YAMLFormatter struct{}

func NewTextFormatter(showMetadata, timestamps bool) *TextFormatter {
    return &TextFormatter{
        ShowMetadata: showMetadata,
        Timestamps:   timestamps,
    }
}

func (tf *TextFormatter) Format(response interface{}) ([]byte, error) {
    var output strings.Builder

    switch resp := response.(type) {
    case *LogResponse:
        return tf.formatSingleNode(resp)
    case *MultiNodeResponse:
        return tf.formatMultiNode(resp)
    default:
        return nil, fmt.Errorf("unsupported response type")
    }
}

func (tf *TextFormatter) formatSingleNode(resp *LogResponse) ([]byte, error) {
    var output strings.Builder

    if tf.ShowMetadata && resp.Metadata.KubeletVersion != "" {
        output.WriteString(fmt.Sprintf("==> Node: %s (Kubelet: %s) <==\n", resp.NodeName, resp.Metadata.KubeletVersion))
        
        if len(resp.Metadata.NodeConditions) > 0 {
            output.WriteString("Node Conditions:\n")
            for _, condition := range resp.Metadata.NodeConditions {
                output.WriteString(fmt.Sprintf("  %s: %s (%s)\n", condition.Type, condition.Status, condition.Reason))
            }
            output.WriteString("\n")
        }

        if len(resp.Metadata.RunningPods) > 0 {
            output.WriteString(fmt.Sprintf("Running Pods: %d\n", len(resp.Metadata.RunningPods)))
            for _, pod := range resp.Metadata.RunningPods {
                status := "NotReady"
                if pod.Ready {
                    status = "Ready"
                }
                output.WriteString(fmt.Sprintf("  %s/%s (%s) - %s\n", pod.Namespace, pod.Name, pod.Phase, status))
            }
            output.WriteString("\n")
        }

        if len(resp.Metadata.RecentEvents) > 0 {
            output.WriteString("Recent Events:\n")
            for _, event := range resp.Metadata.RecentEvents {
                output.WriteString(fmt.Sprintf("  %s [%s] %s: %s\n", 
                    event.Timestamp.Format(time.RFC3339), event.Type, event.Reason, event.Message))
            }
            output.WriteString("\n")
        }
    }

    if tf.Timestamps {
        output.WriteString(fmt.Sprintf("==> Kubelet Logs (Retrieved: %s) <==\n", resp.Timestamp.Format(time.RFC3339)))
    }

    output.WriteString(resp.Logs)

    return []byte(output.String()), nil
}

func (tf *TextFormatter) formatMultiNode(resp *MultiNodeResponse) ([]byte, error) {
    var output strings.Builder

    output.WriteString(fmt.Sprintf("==> Multi-Node Kubelet Logs Summary <==\n"))
    output.WriteString(fmt.Sprintf("Total Nodes: %d, Success: %d, Errors: %d\n", 
        resp.Summary.TotalNodes, resp.Summary.SuccessNodes, resp.Summary.ErrorNodes))
    output.WriteString(fmt.Sprintf("Execution Time: %s\n\n", resp.Summary.ExecutionTime))

    // Show errors first
    if len(resp.Errors) > 0 {
        output.WriteString("==> Errors <==\n")
        for _, err := range resp.Errors {
            output.WriteString(fmt.Sprintf("Node %s: %s\n", err.NodeName, err.Error))
        }
        output.WriteString("\n")
    }

    // Show logs from each node
    for i, nodeResp := range resp.Responses {
        if i > 0 {
            output.WriteString("\n" + strings.Repeat("=", 80) + "\n\n")
        }
        
        nodeOutput, err := tf.formatSingleNode(&nodeResp)
        if err != nil {
            continue
        }
        output.Write(nodeOutput)
    }

    return []byte(output.String()), nil
}

func (jf *JSONFormatter) Format(response interface{}) ([]byte, error) {
    if jf.Pretty {
        return json.MarshalIndent(response, "", "  ")
    }
    return json.Marshal(response)
}

func (yf *YAMLFormatter) Format(response interface{}) ([]byte, error) {
    return yaml.Marshal(response)
}
```

## MCP Server Integration

### Enhanced MCP Tools for Kubelet Logs

```go
// pkg/mcp/kubelet_logs.go
func (s *Server) initKubeletLogsTools() []server.ServerTool {
    return []server.ServerTool{
        {Tool: mcp.NewTool("kubelet_logs_get",
            mcp.WithDescription("Get kubelet logs from a specific node"),
            mcp.WithString("node", mcp.Description("Node name to get kubelet logs from"), mcp.Required()),
            mcp.WithString("cluster", mcp.Description("Cluster to execute on (optional)")),
            mcp.WithString("teleport_profile", mcp.Description("Teleport profile for authentication (optional)")),
            mcp.WithBoolean("follow", mcp.Description("Follow log output")),
            mcp.WithBoolean("timestamps", mcp.Description("Include timestamps")),
            mcp.WithInteger("tail", mcp.Description("Number of lines to tail (-1 for all)")),
            mcp.WithString("since", mcp.Description("Show logs since duration (e.g., '1h', '30m')")),
            mcp.WithBoolean("correlate_pods", mcp.Description("Include running pods information")),
            mcp.WithBoolean("correlate_events", mcp.Description("Include recent node events")),
            mcp.WithString("output", mcp.Description("Output format (text, json, yaml)")),
            mcp.WithTitleAnnotation("Kubelet: Get Logs"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.kubeletLogsGet},

        {Tool: mcp.NewTool("kubelet_logs_all_nodes",
            mcp.WithDescription("Get kubelet logs from all nodes or filtered nodes"),
            mcp.WithString("cluster", mcp.Description("Cluster to execute on (optional)")),
            mcp.WithString("teleport_profile", mcp.Description("Teleport profile for authentication (optional)")),
            mcp.WithString("node_selector", mcp.Description("Label selector to filter nodes")),
            mcp.WithArray("node_names", mcp.Description("Specific node names to target")),
            mcp.WithBoolean("timestamps", mcp.Description("Include timestamps")),
            mcp.WithInteger("tail", mcp.Description("Number of lines to tail from each node")),
            mcp.WithString("since", mcp.Description("Show logs since duration")),
            mcp.WithInteger("max_concurrency", mcp.Description("Maximum concurrent requests (default: 5)")),
            mcp.WithString("output", mcp.Description("Output format (text, json, yaml)")),
            mcp.WithTitleAnnotation("Kubelet: Get All Node Logs"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.kubeletLogsAllNodes},

        {Tool: mcp.NewTool("kubelet_logs_diagnose",
            mcp.WithDescription("Comprehensive kubelet diagnostics including logs, node status, and pod correlation"),
            mcp.WithString("node", mcp.Description("Node name to diagnose"), mcp.Required()),
            mcp.WithString("cluster", mcp.Description("Cluster to execute on (optional)")),
            mcp.WithString("teleport_profile", mcp.Description("Teleport profile for authentication (optional)")),
            mcp.WithString("issue_type", mcp.Description("Type of issue to focus on (scheduling, networking, storage, general)")),
            mcp.WithInteger("lookback_hours", mcp.Description("Hours to look back for logs and events (default: 1)")),
            mcp.WithBoolean("include_pod_logs", mcp.Description("Include logs from failed pods on the node")),
            mcp.WithString("output", mcp.Description("Output format (text, json, yaml)")),
            mcp.WithTitleAnnotation("Kubelet: Comprehensive Diagnostics"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.kubeletLogsDiagnose},

        {Tool: mcp.NewTool("kubelet_logs_stream",
            mcp.WithDescription("Stream kubelet logs from one or more nodes in real-time"),
            mcp.WithArray("nodes", mcp.Description("Node names to stream logs from"), mcp.Required()),
            mcp.WithString("cluster", mcp.Description("Cluster to execute on (optional)")),
            mcp.WithString("teleport_profile", mcp.Description("Teleport profile for authentication (optional)")),
            mcp.WithBoolean("timestamps", mcp.Description("Include timestamps")),
            mcp.WithString("filter", mcp.Description("Filter logs by keyword or regex pattern")),
            mcp.WithInteger("duration_seconds", mcp.Description("Duration to stream in seconds (default: 60)")),
            mcp.WithTitleAnnotation("Kubelet: Stream Logs"),
            mcp.WithReadOnlyHintAnnotation(true),
        ), Handler: s.kubeletLogsStream},
    }
}

func (s *Server) kubeletLogsGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    args := req.GetArguments()
    
    // Extract parameters
    nodeName, _ := args["node"].(string)
    cluster, _ := args["cluster"].(string)
    teleportProfile, _ := args["teleport_profile"].(string)
    follow, _ := args["follow"].(bool)
    timestamps, _ := args["timestamps"].(bool)
    correlatePods, _ := args["correlate_pods"].(bool)
    correlateEvents, _ := args["correlate_events"].(bool)
    output, _ := args["output"].(string)

    // Validate required parameters
    if nodeName == "" {
        return NewTextResult("", fmt.Errorf("node name is required")), nil
    }

    // Get appropriate Kubernetes client (with Teleport/cluster context)
    clientset, err := s.getKubernetesClient(cluster, teleportProfile)
    if err != nil {
        return NewTextResult("", fmt.Errorf("failed to get Kubernetes client: %w", err)), nil
    }

    // Create kubelet logs retriever
    retriever := kubeletlogs.NewLogRetriever(clientset, s.restConfig)

    // Build log request
    logReq := kubeletlogs.LogRequest{
        NodeName:    nodeName,
        Follow:      follow,
        Timestamps:  timestamps,
    }

    // Add optional parameters
    if tail, ok := args["tail"].(float64); ok && tail >= 0 {
        tailLines := int64(tail)
        logReq.TailLines = &tailLines
    }

    if since, ok := args["since"].(string); ok && since != "" {
        duration, err := time.ParseDuration(since)
        if err == nil {
            sinceSeconds := int64(duration.Seconds())
            logReq.SinceSeconds = &sinceSeconds
        }
    }

    // Execute request
    response, err := retriever.GetKubeletLogs(ctx, logReq)
    if err != nil {
        return NewTextResult("", fmt.Errorf("failed to get kubelet logs: %w", err)), nil
    }

    // Format output
    formatter := s.getOutputFormatter(output, correlatePods || correlateEvents, timestamps)
    formattedOutput, err := formatter.Format(response)
    if err != nil {
        return NewTextResult("", fmt.Errorf("failed to format output: %w", err)), nil
    }

    return NewTextResult(string(formattedOutput), nil), nil
}
```

## Security and RBAC Integration

### Required Permissions

```yaml
# kubelet-logs-rbac.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubelet-logs-reader
rules:
# Node access for kubelet logs
- apiGroups: [""]
  resources: ["nodes", "nodes/proxy", "nodes/log"]
  verbs: ["get", "list"]
# Pod information for correlation
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list"]
# Events for correlation
- apiGroups: [""]
  resources: ["events"]
  verbs: ["get", "list"]

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubelet-logs-reader-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kubelet-logs-reader
subjects:
- kind: ServiceAccount
  name: mcp-server
  namespace: mcp-system
```

### Security Considerations

#### 1. API Server Proxy Security
```go
func (lr *LogRetriever) validateAccess(ctx context.Context, nodeName string) error {
    // Verify node exists and is accessible
    _, err := lr.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
    if err != nil {
        return fmt.Errorf("node access denied or not found: %w", err)
    }

    // Additional security checks can be added here
    // - Node label-based access control
    // - Time-based access restrictions
    // - Audit logging

    return nil
}
```

#### 2. Rate Limiting and Throttling
```go
type RateLimiter struct {
    requests map[string][]time.Time
    mutex    sync.RWMutex
    limit    int
    window   time.Duration
}

func (rl *RateLimiter) Allow(identity string) bool {
    rl.mutex.Lock()
    defer rl.mutex.Unlock()

    now := time.Now()
    if rl.requests == nil {
        rl.requests = make(map[string][]time.Time)
    }

    // Clean old requests
    var validRequests []time.Time
    for _, reqTime := range rl.requests[identity] {
        if now.Sub(reqTime) < rl.window {
            validRequests = append(validRequests, reqTime)
        }
    }

    if len(validRequests) >= rl.limit {
        return false
    }

    validRequests = append(validRequests, now)
    rl.requests[identity] = validRequests
    return true
}
```

## Usage Examples

### Basic Kubelet Log Retrieval
```bash
# Get kubelet logs from a specific node
kubectl kubelet-logs worker-node-1

# Get kubelet logs with timestamps and correlation
kubectl kubelet-logs worker-node-1 --timestamps --correlate-with-pods --correlate-with-events

# Follow kubelet logs in real-time
kubectl kubelet-logs worker-node-1 --follow

# Get recent kubelet logs (last hour)
kubectl kubelet-logs worker-node-1 --since=1h --tail=100
```

### Multi-Node Operations
```bash
# Get kubelet logs from all nodes
kubectl kubelet-logs --all-nodes

# Get kubelet logs from nodes with specific labels
kubectl kubelet-logs --all-nodes --selector="node-role.kubernetes.io/worker="

# Get kubelet logs from specific nodes
kubectl kubelet-logs node-1,node-2,node-3 --output=json
```

### Diagnostic Scenarios
```bash
# Comprehensive node diagnostics
kubectl kubelet-logs worker-node-1 --correlate-with-pods --correlate-with-events --show-node-conditions

# Debug scheduling issues
kubectl kubelet-logs --all-nodes --since=30m | grep -i "failed.*schedule\|evict\|oom"

# Debug networking issues
kubectl kubelet-logs worker-node-1 --since=1h | grep -i "cni\|network\|dns"

# Debug storage issues
kubectl kubelet-logs worker-node-1 --since=2h | grep -i "volume\|mount\|storage\|pv"
```

### AI Agent Integration
```json
// Get kubelet logs via MCP
{"method": "tools/call", "params": {"name": "kubelet_logs_get", "arguments": {"node": "worker-node-1", "correlate_pods": true, "timestamps": true}}}

// Multi-node kubelet logs
{"method": "tools/call", "params": {"name": "kubelet_logs_all_nodes", "arguments": {"node_selector": "node-role.kubernetes.io/worker=", "since": "1h"}}}

// Comprehensive diagnostics
{"method": "tools/call", "params": {"name": "kubelet_logs_diagnose", "arguments": {"node": "worker-node-1", "issue_type": "scheduling", "lookback_hours": 2}}}

// Stream logs for monitoring
{"method": "tools/call", "params": {"name": "kubelet_logs_stream", "arguments": {"nodes": ["worker-node-1", "worker-node-2"], "duration_seconds": 300}}}
```

## Deployment and Distribution

### Plugin Installation

#### Via Krew
```yaml
# .krew.yaml
apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: kubelet-logs
spec:
  version: v1.0.0
  homepage: https://github.com/netSkopePlatformEng/kubectl-kubelet-logs
  shortDescription: Get kubelet logs from Kubernetes nodes
  description: |
    kubectl-kubelet-logs provides secure access to kubelet logs through the 
    Kubernetes API without requiring direct node access. Supports multi-node 
    operations, log correlation, and various output formats.
  platforms:
  - selector:
      matchLabels:
        os: linux
        arch: amd64
    uri: https://github.com/netSkopePlatformEng/kubectl-kubelet-logs/releases/download/v1.0.0/kubectl-kubelet-logs-linux-amd64.tar.gz
    sha256: "sha256sum_here"
    bin: kubectl-kubelet-logs
  - selector:
      matchLabels:
        os: darwin
        arch: amd64
    uri: https://github.com/netSkopePlatformEng/kubectl-kubelet-logs/releases/download/v1.0.0/kubectl-kubelet-logs-darwin-amd64.tar.gz
    sha256: "sha256sum_here"
    bin: kubectl-kubelet-logs
  - selector:
      matchLabels:
        os: windows
        arch: amd64
    uri: https://github.com/netSkopePlatformEng/kubectl-kubelet-logs/releases/download/v1.0.0/kubectl-kubelet-logs-windows-amd64.zip
    sha256: "sha256sum_here"
    bin: kubectl-kubelet-logs.exe
```

#### Direct Installation
```bash
# Install via krew
kubectl krew install kubelet-logs

# Install manually
curl -LO https://github.com/netSkopePlatformEng/kubectl-kubelet-logs/releases/latest/download/kubectl-kubelet-logs-linux-amd64.tar.gz
tar -xzf kubectl-kubelet-logs-linux-amd64.tar.gz
sudo mv kubectl-kubelet-logs /usr/local/bin/
```

### Docker Integration
```dockerfile
# Add to existing MCP server Dockerfile
RUN curl -LO https://github.com/netSkopePlatformEng/kubectl-kubelet-logs/releases/latest/download/kubectl-kubelet-logs-linux-amd64.tar.gz \
    && tar -xzf kubectl-kubelet-logs-linux-amd64.tar.gz \
    && mv kubectl-kubelet-logs /usr/local/bin/ \
    && rm kubectl-kubelet-logs-linux-amd64.tar.gz

# Or install via krew
RUN kubectl krew install kubelet-logs
```

## Benefits and Use Cases

### For AI Agents
- **Holistic Debugging**: Correlate kubelet logs with API events and pod status
- **Node Health Monitoring**: Continuous monitoring of kubelet health across clusters
- **Automated Diagnostics**: AI-driven analysis of kubelet issues and patterns
- **Multi-Cluster Visibility**: Consistent kubelet log access across all environments

### For Platform Engineers
- **Simplified Troubleshooting**: No need for direct node SSH access
- **Centralized Logging**: Kubelet logs accessible through standard Kubernetes RBAC
- **Correlation Capabilities**: Built-in correlation with pods, events, and node conditions
- **Multi-Node Operations**: Efficient troubleshooting across entire clusters

### For Security Teams
- **Audit Trail**: All kubelet log access logged through Kubernetes audit system
- **RBAC Integration**: Fine-grained access control using existing Kubernetes permissions
- **No Direct Access**: Eliminates need for SSH access to nodes
- **Compliance**: Meets enterprise security requirements for log access

## Implementation Timeline

### Phase 1: Core Plugin Development (Weeks 1-2)
- Basic kubectl plugin structure and CLI interface
- Single-node kubelet log retrieval via API proxy
- Text output formatting and basic error handling
- Unit tests and initial documentation

### Phase 2: Advanced Features (Weeks 3-4)  
- Multi-node support with concurrency control
- Log correlation with pods, events, and node conditions
- Multiple output formats (JSON, YAML)
- Rate limiting and security enhancements

### Phase 3: MCP Integration (Weeks 5-6)
- MCP server tool integration
- Teleport and multi-cluster awareness
- Streaming capabilities and real-time monitoring
- Comprehensive diagnostics and analysis tools

### Phase 4: Production Readiness (Weeks 7-8)
- Krew packaging and distribution
- Docker integration and deployment automation
- Performance optimization and testing
- Documentation and user guides

## Success Metrics

1. **Functionality**: Successful kubelet log retrieval from all node types
2. **Security**: Full RBAC integration and audit compliance
3. **Performance**: Sub-second response for single-node queries
4. **Scalability**: Support for 100+ node clusters with reasonable performance
5. **Integration**: Seamless operation with existing MCP server and Teleport
6. **Adoption**: Easy installation and usage for both AI agents and human operators

This kubectl-kubelet-logs plugin fills a critical gap in Kubernetes observability, providing AI agents and operators with secure, comprehensive access to kubelet diagnostics while maintaining enterprise security standards and enabling powerful correlation capabilities for effective troubleshooting.