package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/output"
)

func (s *Server) initResources() []server.ServerTool {
	commonApiVersion := "v1 Pod, v1 Service, v1 Node, apps/v1 Deployment, networking.k8s.io/v1 Ingress"
	// Only check for OpenShift if we have an active cluster
	if s.k != nil && s.k.IsOpenShift(context.Background()) {
		commonApiVersion += ", route.openshift.io/v1 Route"
	}
	commonApiVersion = fmt.Sprintf("(common apiVersion and kind include: %s)", commonApiVersion)
	return []server.ServerTool{
		{Tool: mcp.NewTool("resources_list",
			mcp.WithDescription("List Kubernetes resources and objects in the current cluster by providing their apiVersion and kind and optionally the namespace and label selector\n"+
				commonApiVersion),
			mcp.WithString("apiVersion",
				mcp.Description("apiVersion of the resources (examples of valid apiVersion are: v1, apps/v1, networking.k8s.io/v1)"),
				mcp.Required(),
			),
			mcp.WithString("kind",
				mcp.Description("kind of the resources (examples of valid kind are: Pod, Service, Deployment, Ingress)"),
				mcp.Required(),
			),
			mcp.WithString("namespace",
				mcp.Description("Optional Namespace to retrieve the namespaced resources from (ignored in case of cluster scoped resources). If not provided, will list resources from all namespaces")),
			mcp.WithString("labelSelector",
				mcp.Description("Optional Kubernetes label selector (e.g. 'app=myapp,env=prod' or 'app in (myapp,yourapp)'), use this option when you want to filter the pods by label"), mcp.Pattern("([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]")),
			// Tool annotations
			mcp.WithTitleAnnotation("Resources: List"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.resourcesList},
		{Tool: mcp.NewTool("resources_get",
			mcp.WithDescription("Get a Kubernetes resource in the current cluster by providing its apiVersion, kind, optionally the namespace, and its name\n"+
				commonApiVersion),
			mcp.WithString("apiVersion",
				mcp.Description("apiVersion of the resource (examples of valid apiVersion are: v1, apps/v1, networking.k8s.io/v1)"),
				mcp.Required(),
			),
			mcp.WithString("kind",
				mcp.Description("kind of the resource (examples of valid kind are: Pod, Service, Deployment, Ingress)"),
				mcp.Required(),
			),
			mcp.WithString("namespace",
				mcp.Description("Optional Namespace to retrieve the namespaced resource from (ignored in case of cluster scoped resources). If not provided, will get resource from configured namespace"),
			),
			mcp.WithString("name", mcp.Description("Name of the resource"), mcp.Required()),
			// Tool annotations
			mcp.WithTitleAnnotation("Resources: Get"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.resourcesGet},
		{Tool: mcp.NewTool("resources_create_or_update",
			mcp.WithDescription("Create or update a Kubernetes resource in the current cluster by providing a YAML or JSON representation of the resource\n"+
				commonApiVersion),
			mcp.WithString("resource",
				mcp.Description("A JSON or YAML containing a representation of the Kubernetes resource. Should include top-level fields such as apiVersion,kind,metadata, and spec"),
				mcp.Required(),
			),
			// Tool annotations
			mcp.WithTitleAnnotation("Resources: Create or Update"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.resourcesCreateOrUpdate},
		{Tool: mcp.NewTool("resources_delete",
			mcp.WithDescription("Delete a Kubernetes resource in the current cluster by providing its apiVersion, kind, optionally the namespace, and its name\n"+
				commonApiVersion),
			mcp.WithString("apiVersion",
				mcp.Description("apiVersion of the resource (examples of valid apiVersion are: v1, apps/v1, networking.k8s.io/v1)"),
				mcp.Required(),
			),
			mcp.WithString("kind",
				mcp.Description("kind of the resource (examples of valid kind are: Pod, Service, Deployment, Ingress)"),
				mcp.Required(),
			),
			mcp.WithString("namespace",
				mcp.Description("Optional Namespace to delete the namespaced resource from (ignored in case of cluster scoped resources). If not provided, will delete resource from configured namespace"),
			),
			mcp.WithString("name", mcp.Description("Name of the resource"), mcp.Required()),
			// Tool annotations
			mcp.WithTitleAnnotation("Resources: Delete"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
		), Handler: s.resourcesDelete},
	}
}

func (s *Server) resourcesList(ctx context.Context, ctr mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace := ctr.GetArguments()["namespace"]
	if namespace == nil {
		namespace = ""
	}
	labelSelector := ctr.GetArguments()["labelSelector"]
	resourceListOptions := kubernetes.ResourceListOptions{
		AsTable: s.configuration.ListOutput.AsTable(),
	}

	if labelSelector != nil {
		l, ok := labelSelector.(string)
		if !ok {
			return NewTextResult("", fmt.Errorf("labelSelector is not a string")), nil
		}
		resourceListOptions.LabelSelector = l
	}
	gvk, err := parseGroupVersionKind(ctr.GetArguments())
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to list resources, %s", err)), nil
	}

	ns, ok := namespace.(string)
	if !ok {
		return NewTextResult("", fmt.Errorf("namespace is not a string")), nil
	}

	// Get the appropriate Kubernetes instance
	var k8s *kubernetes.Kubernetes

	if s.configuration.StaticConfig.IsMultiClusterEnabled() && s.clusterManager != nil {
		// In multi-cluster mode, create a fresh manager for this operation (kubectl-style)
		activeCluster := s.clusterManager.GetActiveCluster()
		klog.Infof("resourcesList: Multi-cluster mode, active cluster: %s", activeCluster)

		// Use WithFreshManager to ensure proper cleanup
		var result *mcp.CallToolResult
		err = s.clusterManager.WithFreshManager(func(manager *kubernetes.Manager) error {
			// Log the manager's server
			if restConfig, err := manager.ToRESTConfig(); err == nil && restConfig != nil {
				klog.Infof("resourcesList: Fresh manager for cluster %s, server: %s", activeCluster, restConfig.Host)
			}

			// Create a Kubernetes instance with the fresh manager
			k8s, err = manager.Derived(ctx)
			if err != nil {
				return err
			}

			// Execute the operation with the fresh client
			ret, err := k8s.ResourcesList(ctx, gvk, ns, resourceListOptions)
			if err != nil {
				errorMsg := s.formatKubernetesError(err)
				result = NewTextResult("", fmt.Errorf("failed to list resources: %s", errorMsg))
				return nil // Don't return error, we've already formatted it
			}
			result = NewTextResult(s.configuration.ListOutput.PrintObj(ret))
			return nil
		})

		if err != nil {
			return nil, err
		}
		return result, nil
	} else {
		// In single-cluster mode, use the regular derived instance
		k8s, err = s.k.Derived(ctx)
		if err != nil {
			return nil, err
		}
	}

	ret, err := k8s.ResourcesList(ctx, gvk, ns, resourceListOptions)
	if err != nil {
		errorMsg := s.formatKubernetesError(err)
		return NewTextResult("", fmt.Errorf("failed to list resources: %s", errorMsg)), nil
	}
	return NewTextResult(s.configuration.ListOutput.PrintObj(ret)), nil
}

func (s *Server) resourcesGet(ctx context.Context, ctr mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace := ctr.GetArguments()["namespace"]
	if namespace == nil {
		namespace = ""
	}
	gvk, err := parseGroupVersionKind(ctr.GetArguments())
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to get resource, %s", err)), nil
	}
	name := ctr.GetArguments()["name"]
	if name == nil {
		return NewTextResult("", errors.New("failed to get resource, missing argument name")), nil
	}

	ns, ok := namespace.(string)
	if !ok {
		return NewTextResult("", fmt.Errorf("namespace is not a string")), nil
	}

	n, ok := name.(string)
	if !ok {
		return NewTextResult("", fmt.Errorf("name is not a string")), nil
	}

	derived, err := s.k.Derived(ctx)
	if err != nil {
		return nil, err
	}
	ret, err := derived.ResourcesGet(ctx, gvk, ns, n)
	if err != nil {
		errorMsg := s.formatKubernetesError(err)
		return NewTextResult("", fmt.Errorf("failed to get resource: %s", errorMsg)), nil
	}
	return NewTextResult(output.MarshalYaml(ret)), nil
}

func (s *Server) resourcesCreateOrUpdate(ctx context.Context, ctr mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resource := ctr.GetArguments()["resource"]
	if resource == nil || resource == "" {
		return NewTextResult("", errors.New("failed to create or update resources, missing argument resource")), nil
	}

	r, ok := resource.(string)
	if !ok {
		return NewTextResult("", fmt.Errorf("resource is not a string")), nil
	}

	derived, err := s.k.Derived(ctx)
	if err != nil {
		return nil, err
	}
	resources, err := derived.ResourcesCreateOrUpdate(ctx, r)
	if err != nil {
		// Enhanced error handling for better user experience
		errorMsg := s.formatKubernetesError(err)
		return NewTextResult("", fmt.Errorf("failed to create or update resources: %s", errorMsg)), nil
	}
	marshalledYaml, err := output.MarshalYaml(resources)
	if err != nil {
		err = fmt.Errorf("failed to create or update resources:: %v", err)
	}
	return NewTextResult("# The following resources (YAML) have been created or updated successfully\n"+marshalledYaml, err), nil
}

func (s *Server) resourcesDelete(ctx context.Context, ctr mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace := ctr.GetArguments()["namespace"]
	if namespace == nil {
		namespace = ""
	}
	gvk, err := parseGroupVersionKind(ctr.GetArguments())
	if err != nil {
		return NewTextResult("", fmt.Errorf("failed to delete resource, %s", err)), nil
	}
	name := ctr.GetArguments()["name"]
	if name == nil {
		return NewTextResult("", errors.New("failed to delete resource, missing argument name")), nil
	}

	ns, ok := namespace.(string)
	if !ok {
		return NewTextResult("", fmt.Errorf("namespace is not a string")), nil
	}

	n, ok := name.(string)
	if !ok {
		return NewTextResult("", fmt.Errorf("name is not a string")), nil
	}

	derived, err := s.k.Derived(ctx)
	if err != nil {
		return nil, err
	}
	err = derived.ResourcesDelete(ctx, gvk, ns, n)
	if err != nil {
		errorMsg := s.formatKubernetesError(err)
		return NewTextResult("", fmt.Errorf("failed to delete resource: %s", errorMsg)), nil
	}
	return NewTextResult("Resource deleted successfully", err), nil
}

func parseGroupVersionKind(arguments map[string]interface{}) (*schema.GroupVersionKind, error) {
	apiVersion := arguments["apiVersion"]
	if apiVersion == nil {
		return nil, errors.New("missing argument apiVersion")
	}
	kind := arguments["kind"]
	if kind == nil {
		return nil, errors.New("missing argument kind")
	}

	a, ok := apiVersion.(string)
	if !ok {
		return nil, fmt.Errorf("name is not a string")
	}

	gv, err := schema.ParseGroupVersion(a)
	if err != nil {
		return nil, errors.New("invalid argument apiVersion")
	}
	return &schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: kind.(string)}, nil
}

// formatKubernetesError formats Kubernetes API errors into more user-friendly messages
func (s *Server) formatKubernetesError(err error) string {
	if err == nil {
		return "unknown error"
	}

	// Check if it's a Kubernetes API error
	if k8serrors.IsConflict(err) {
		// Handle field manager conflicts and other conflicts
		errorMsg := err.Error()
		if strings.Contains(errorMsg, "field is owned by") {
			return fmt.Sprintf("Field management conflict: %s\n\nThis usually happens when a resource field was previously managed by a different tool (like kubectl). To resolve this:\n1. Use 'kubectl apply --force-conflicts --server-side' for server-side apply, or\n2. Use 'kubectl replace' instead of apply, or\n3. Remove the conflicting field from your YAML and apply again", errorMsg)
		}
		return fmt.Sprintf("Resource conflict: %s", errorMsg)
	}

	if k8serrors.IsAlreadyExists(err) {
		return fmt.Sprintf("Resource already exists: %s\n\nTip: Use 'kubectl apply' instead of 'kubectl create', or delete the existing resource first.", err.Error())
	}

	if k8serrors.IsNotFound(err) {
		return fmt.Sprintf("Resource not found: %s\n\nTip: Check if the namespace exists and the resource name is correct.", err.Error())
	}

	if k8serrors.IsForbidden(err) {
		return fmt.Sprintf("Access forbidden: %s\n\nTip: Check your RBAC permissions or authentication credentials.", err.Error())
	}

	if k8serrors.IsUnauthorized(err) {
		return fmt.Sprintf("Authentication failed: %s\n\nTip: Check your kubeconfig credentials or token.", err.Error())
	}

	if k8serrors.IsInvalid(err) {
		errorMsg := err.Error()
		return fmt.Sprintf("Invalid resource specification: %s\n\nTip: Check your YAML syntax and field values.", errorMsg)
	}

	if k8serrors.IsTimeout(err) {
		return fmt.Sprintf("Operation timed out: %s\n\nTip: The cluster may be slow to respond. Try again or check cluster health.", err.Error())
	}

	if k8serrors.IsServerTimeout(err) {
		return fmt.Sprintf("Server timeout: %s\n\nTip: The Kubernetes API server is taking too long to respond. Check cluster status.", err.Error())
	}

	if k8serrors.IsServiceUnavailable(err) {
		return fmt.Sprintf("Service unavailable: %s\n\nTip: The Kubernetes API server may be overloaded or temporarily unavailable.", err.Error())
	}

	// For any other error, provide the original message with context
	errorMsg := err.Error()
	if strings.Contains(errorMsg, "connection refused") {
		return fmt.Sprintf("Connection failed: %s\n\nTip: Check if the cluster is running and accessible.", errorMsg)
	}

	if strings.Contains(errorMsg, "no such host") {
		return fmt.Sprintf("Network error: %s\n\nTip: Check your kubeconfig server URL and network connectivity.", errorMsg)
	}

	// Return the original error for any unhandled cases
	return errorMsg
}
