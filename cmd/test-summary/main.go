package main

import (
	"fmt"
	"strings"
)

// This demonstrates how summarizeResult works with different types of outputs

func summarizeResult(result interface{}) string {
	if result == nil {
		return "No data"
	}

	// Convert to string and parse for key information
	resultStr := fmt.Sprintf("%v", result)

	// Count lines (helpful for table output)
	lines := strings.Split(strings.TrimSpace(resultStr), "\n")
	lineCount := len(lines)

	// Try to detect table format (NAME, NAMESPACE, STATUS, etc.)
	if lineCount > 1 && (strings.Contains(lines[0], "NAME") || strings.Contains(lines[0], "NAMESPACE")) {
		// This looks like kubectl table output
		// Count actual data rows (excluding header and separators)
		dataRows := 0
		for i, line := range lines {
			if i == 0 {
				continue // Skip header
			}
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "---") {
				dataRows++
			}
		}
		if dataRows > 0 {
			return fmt.Sprintf("%d resources (table format)", dataRows)
		}
	}

	// Try to extract key metrics based on common patterns
	// Look for "Items:" to count resources
	if strings.Contains(resultStr, "Items:") {
		// Count number of items (e.g., nodes, pods)
		itemCount := 0
		for _, line := range lines {
			if strings.Contains(line, "Name:") || strings.Contains(line, "- name:") {
				itemCount++
			}
		}
		if itemCount > 0 {
			return fmt.Sprintf("%d items found", itemCount)
		}
	}

	// Look for common success/status messages
	if strings.Contains(resultStr, "successfully") || strings.Contains(resultStr, "SUCCESS") {
		if len(resultStr) <= 200 {
			return resultStr
		}
		return fmt.Sprintf("%s... (success)", resultStr[:150])
	}

	// For shorter results, just show them entirely
	if len(resultStr) <= 200 {
		return resultStr
	}

	// For longer results, show first line + count
	if lineCount > 5 {
		return fmt.Sprintf("%s... (%d lines total, use include_full_results=true for complete output)", lines[0], lineCount)
	}

	// Fallback: show first 200 chars with ellipsis
	return fmt.Sprintf("%s... (use include_full_results=true for complete output)", resultStr[:200])
}

func main() {
	fmt.Println("=== Summary Examples ===\n")

	// Example 0: Actual kubectl get nodes table format
	kubectlNodesTable := `NAME                    STATUS   ROLES           AGE    VERSION    INTERNAL-IP     EXTERNAL-IP   OS-IMAGE             KERNEL-VERSION      CONTAINER-RUNTIME
c1-sjc3-master-001      Ready    control-plane   145d   v1.24.17   192.168.1.10    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-master-002      Ready    control-plane   145d   v1.24.17   192.168.1.11    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-master-003      Ready    control-plane   145d   v1.24.17   192.168.1.12    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-worker-001      Ready    worker          145d   v1.24.17   192.168.1.20    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-worker-002      Ready    worker          145d   v1.24.17   192.168.1.21    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-worker-003      Ready    worker          145d   v1.24.17   192.168.1.22    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24`

	fmt.Println("0. Kubectl Get Nodes Table (ACTUAL FORMAT):")
	fmt.Println("   Original:", len(kubectlNodesTable), "chars")
	fmt.Println("   Summary:", summarizeResult(kubectlNodesTable))
	fmt.Println()

	// Example 1: List of nodes (typical resources_list output)
	nodesOutput := `Items:
- Name: node-1
  Status: Ready
  Version: v1.24.17
  IP: 10.0.1.10
- Name: node-2
  Status: Ready
  Version: v1.24.17
  IP: 10.0.1.11
- Name: node-3
  Status: Ready
  Version: v1.24.17
  IP: 10.0.1.12`

	fmt.Println("1. Nodes List Output:")
	fmt.Println("   Original:", len(nodesOutput), "chars")
	fmt.Println("   Summary:", summarizeResult(nodesOutput))
	fmt.Println()

	// Example 2: Simple success message
	simpleOutput := "Operation completed successfully"
	fmt.Println("2. Simple Success Message:")
	fmt.Println("   Original:", simpleOutput)
	fmt.Println("   Summary:", summarizeResult(simpleOutput))
	fmt.Println()

	// Example 3: Large YAML output (typical kubectl get output)
	largeYaml := `apiVersion: v1
kind: NodeList
metadata:
  resourceVersion: "12345678"
items:
- apiVersion: v1
  kind: Node
  metadata:
    name: c1-sjc3-worker-001
    creationTimestamp: "2024-01-15T10:30:00Z"
    labels:
      kubernetes.io/hostname: c1-sjc3-worker-001
      node-role.kubernetes.io/worker: ""
  spec:
    podCIDR: 10.244.1.0/24
  status:
    conditions:
    - type: Ready
      status: "True"
    addresses:
    - type: InternalIP
      address: 192.168.1.10
    - type: Hostname
      address: c1-sjc3-worker-001
    capacity:
      cpu: "16"
      memory: 64Gi
      pods: "110"
- apiVersion: v1
  kind: Node
  metadata:
    name: c1-sjc3-worker-002
    creationTimestamp: "2024-01-15T10:35:00Z"
  ... (continues for many more nodes)`

	fmt.Println("3. Large YAML Output:")
	fmt.Println("   Original:", len(largeYaml), "chars")
	fmt.Println("   Summary:", summarizeResult(largeYaml))
	fmt.Println()

	// Example 4: Pod list with "Items:" pattern
	podListOutput := `Total: 15 pods
Items:
- Name: nginx-deployment-abc123
  Namespace: default
  Status: Running
- Name: redis-cache-def456
  Namespace: default
  Status: Running
- Name: postgres-db-ghi789
  Namespace: databases
  Status: Running
- Name: api-server-jkl012
  Namespace: production
  Status: Running
- Name: worker-queue-mno345
  Namespace: production
  Status: Running`

	fmt.Println("4. Pod List Output:")
	fmt.Println("   Original:", len(podListOutput), "chars")
	fmt.Println("   Summary:", summarizeResult(podListOutput))
	fmt.Println()

	// Example 5: Error message (short)
	errorOutput := "Error: connection timeout to API server"
	fmt.Println("5. Error Message:")
	fmt.Println("   Original:", errorOutput)
	fmt.Println("   Summary:", summarizeResult(errorOutput))
	fmt.Println()

	// Example 6: Deployment status (medium length)
	deploymentStatus := `Deployment: nginx-ingress
Replicas: 3/3 ready
Image: nginx:1.21
Strategy: RollingUpdate
Conditions:
  - Available: True
  - Progressing: True`

	fmt.Println("6. Deployment Status:")
	fmt.Println("   Original:", deploymentStatus)
	fmt.Println("   Summary:", summarizeResult(deploymentStatus))
	fmt.Println()

	// Example 7: Very long output without Items pattern
	longOutput := strings.Repeat("This is a very long output that contains lots of configuration data and settings. ", 20)
	fmt.Println("7. Very Long Output (no Items pattern):")
	fmt.Println("   Original:", len(longOutput), "chars")
	fmt.Println("   Summary:", summarizeResult(longOutput))
	fmt.Println()

	// Example 8: Namespace list
	namespaceList := `Items:
- Name: default
  Status: Active
- Name: kube-system
  Status: Active
- Name: kube-public
  Status: Active
- Name: production
  Status: Active
- Name: staging
  Status: Active`

	fmt.Println("8. Namespace List:")
	fmt.Println("   Original:", len(namespaceList), "chars")
	fmt.Println("   Summary:", summarizeResult(namespaceList))
	fmt.Println()

	fmt.Println("\n=== Example Job Results Output ===\n")

	// Simulate what the actual output would look like
	fmt.Println("Job Results (Page 1 of 15)\n")
	fmt.Println("Job ID: 6fde8a6f-9c3e-48d3-b711-a3625a6f48f6")
	fmt.Println("State: succeeded\n")

	// Additional realistic example: Pod list in table format
	kubectlPodsTable := `NAME                                READY   STATUS    RESTARTS   AGE
nginx-deployment-7d64f8f7c4-abc12   1/1     Running   0          23d
nginx-deployment-7d64f8f7c4-def34   1/1     Running   0          23d
redis-cache-6d5f8b9c8d-ghi56        1/1     Running   1          15d
postgres-db-5c4d3b2a1f-jkl78        1/1     Running   0          42d
api-server-8f7e6d5c4b-mno90         2/2     Running   0          7d`

	// Show a few example clusters
	clusters := []struct {
		name    string
		success bool
		ms      int
		result  string
	}{
		{"c1-am2", true, 234, kubectlNodesTable},
		{"c1-sjc3", true, 187, kubectlPodsTable},
		{"c1-sv5", false, 5000, "Error: connection timeout to API server"},
		{"c1-lhr1", true, 298, largeYaml},
		{"c1-fra1", true, 312, namespaceList},
		{"c1-iad1", true, 445, simpleOutput},
		{"c1-dfw1", true, 256, deploymentStatus},
	}

	for _, cluster := range clusters {
		status := "✅"
		if !cluster.success {
			status = "❌"
		}
		fmt.Printf("%s %s - %dms\n", status, cluster.name, cluster.ms)
		if !cluster.success {
			fmt.Printf("   Error: %s\n", cluster.result)
		} else {
			fmt.Printf("   Summary: %s\n", summarizeResult(cluster.result))
		}
	}

	fmt.Println("\nShowing 5 of 147 results")
	fmt.Println("More results available. Use cursor: MTU=")
	fmt.Println("\nSummary:")
	fmt.Println("- Total: 147")
	fmt.Println("- Succeeded: 143")
	fmt.Println("- Failed: 4")
	fmt.Println("- Total Duration: 42387ms (avg: 288ms)")
}
