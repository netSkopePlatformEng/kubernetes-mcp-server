# Summary Examples for Different Kubernetes API Calls

## How Summarization Works

The `summarizeResult()` function analyzes the output and applies intelligent rules:

### Rule 1: Table Format Detection (Most Common)
**Pattern:** Output starts with column headers like "NAME", "NAMESPACE", "STATUS", etc.
**Action:** Count rows and return "{N} resources (table format)"

### Rule 2: Items List Detection
**Pattern:** Output contains "Items:" or "- name:" patterns
**Action:** Count items and return "{N} items found"

### Rule 3: Success Messages
**Pattern:** Output contains "successfully" or "SUCCESS"
**Action:** Show the message (truncated if > 200 chars)

### Rule 4: Short Messages (≤200 chars)
**Action:** Show the entire message

### Rule 5: Long Messages (>200 chars)
**Action:** Show first line + line count or first 200 chars

---

## Examples by Operation Type

### 1. `resources_list` - List Nodes
**API Call:**
```
start_clusters_exec(
  operation: "resources_list",
  arguments: {"apiVersion": "v1", "kind": "Node"}
)
```

**Raw Output (1103 chars):**
```
NAME                    STATUS   ROLES           AGE    VERSION    INTERNAL-IP     EXTERNAL-IP   OS-IMAGE             KERNEL-VERSION      CONTAINER-RUNTIME
c1-sjc3-master-001      Ready    control-plane   145d   v1.24.17   192.168.1.10    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-master-002      Ready    control-plane   145d   v1.24.17   192.168.1.11    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-master-003      Ready    control-plane   145d   v1.24.17   192.168.1.12    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-worker-001      Ready    worker          145d   v1.24.17   192.168.1.20    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-worker-002      Ready    worker          145d   v1.24.17   192.168.1.21    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
c1-sjc3-worker-003      Ready    worker          145d   v1.24.17   192.168.1.22    <none>        Ubuntu 20.04.6 LTS   5.4.0-166-generic   containerd://1.6.24
```

**Summary:** `6 resources (table format)` ✅

---

### 2. `pods_list_in_namespace` - List Pods
**API Call:**
```
start_clusters_exec(
  operation: "pods_list_in_namespace",
  arguments: {"namespace": "kube-system"}
)
```

**Raw Output (850 chars):**
```
NAME                                READY   STATUS    RESTARTS   AGE
coredns-6d4b75cb6d-abc12           1/1     Running   0          145d
coredns-6d4b75cb6d-def34           1/1     Running   0          145d
etcd-master-001                    1/1     Running   0          145d
etcd-master-002                    1/1     Running   0          145d
kube-apiserver-master-001          1/1     Running   0          145d
kube-controller-manager-001        1/1     Running   0          145d
kube-proxy-ghi56                   1/1     Running   0          145d
kube-proxy-jkl78                   1/1     Running   0          145d
kube-scheduler-master-001          1/1     Running   0          145d
```

**Summary:** `9 resources (table format)` ✅

---

### 3. `resources_get` - Get Single Resource
**API Call:**
```
start_clusters_exec(
  operation: "resources_get",
  arguments: {
    "apiVersion": "v1",
    "kind": "Service",
    "name": "kubernetes",
    "namespace": "default"
  }
)
```

**Raw Output (large YAML, 2500+ chars):**
```
apiVersion: v1
kind: Service
metadata:
  creationTimestamp: "2024-06-01T10:30:00Z"
  labels:
    component: apiserver
    provider: kubernetes
  name: kubernetes
  namespace: default
  resourceVersion: "123456"
  uid: abc-123-def-456
spec:
  clusterIP: 10.96.0.1
  clusterIPs:
  - 10.96.0.1
  internalTrafficPolicy: Cluster
  ipFamilies:
  - IPv4
  ipFamilyPolicy: SingleStack
  ports:
  - name: https
    port: 443
    protocol: TCP
    targetPort: 6443
  sessionAffinity: None
  type: ClusterIP
status:
  loadBalancer: {}
```

**Summary:** `apiVersion: v1... (34 lines total, use include_full_results=true for complete output)` ✅

---

### 4. `namespaces_list` - List Namespaces
**API Call:**
```
start_clusters_exec(
  operation: "namespaces_list"
)
```

**Raw Output (320 chars):**
```
NAME              STATUS   AGE
cattle-system     Active   145d
default           Active   145d
kube-node-lease   Active   145d
kube-public       Active   145d
kube-system       Active   145d
production        Active   89d
staging           Active   89d
```

**Summary:** `7 resources (table format)` ✅

---

### 5. `pods_delete` - Delete a Pod
**API Call:**
```
start_clusters_exec(
  operation: "pods_delete",
  arguments: {
    "name": "nginx-abc123",
    "namespace": "default"
  }
)
```

**Raw Output (45 chars):**
```
pod "nginx-abc123" deleted successfully
```

**Summary:** `pod "nginx-abc123" deleted successfully` ✅ (shown in full because it contains "successfully")

---

### 6. `pods_exec` - Execute Command in Pod
**API Call:**
```
start_clusters_exec(
  operation: "pods_exec",
  arguments: {
    "name": "nginx-abc123",
    "namespace": "default",
    "command": ["df", "-h"]
  }
)
```

**Raw Output (500 chars):**
```
Filesystem      Size  Used Avail Use% Mounted on
overlay         100G   45G   55G  45% /
tmpfs            64M     0   64M   0% /dev
tmpfs           7.8G     0  7.8G   0% /sys/fs/cgroup
/dev/sda1       100G   45G   55G  45% /etc/hosts
shm              64M     0   64M   0% /dev/shm
tmpfs           7.8G   12K  7.8G   1% /run/secrets/kubernetes.io/serviceaccount
tmpfs           7.8G     0  7.8G   0% /proc/acpi
tmpfs           7.8G     0  7.8G   0% /sys/firmware
```

**Summary:** `Filesystem      Size  Used Avail Use% Mounted on... (9 lines total, use include_full_results=true for complete output)` ✅

---

### 7. `pods_log` - Get Pod Logs
**API Call:**
```
start_clusters_exec(
  operation: "pods_log",
  arguments: {
    "name": "nginx-abc123",
    "namespace": "default"
  }
)
```

**Raw Output (3000+ chars of logs):**
```
2025-10-16T23:45:12.123Z [INFO] Starting nginx server
2025-10-16T23:45:12.456Z [INFO] Configuration loaded from /etc/nginx/nginx.conf
2025-10-16T23:45:12.789Z [INFO] Listening on port 80
2025-10-16T23:45:15.123Z [INFO] GET /health - 200 OK
2025-10-16T23:45:18.456Z [INFO] GET /api/v1/users - 200 OK
... (100+ more lines)
```

**Summary:** `2025-10-16T23:45:12.123Z [INFO] Starting nginx server... (103 lines total, use include_full_results=true for complete output)` ✅

---

### 8. `resources_create_or_update` - Create/Update Resource
**API Call:**
```
start_clusters_exec(
  operation: "resources_create_or_update",
  arguments: {
    "resource": "{...yaml content...}"
  }
)
```

**Raw Output (35 chars):**
```
deployment "nginx" updated successfully
```

**Summary:** `deployment "nginx" updated successfully` ✅

---

### 9. `helm_list` - List Helm Releases
**API Call:**
```
start_clusters_exec(
  operation: "helm_list"
)
```

**Raw Output (600 chars):**
```
NAME            NAMESPACE       REVISION  UPDATED                                 STATUS    CHART           APP VERSION
nginx-ingress   default         3         2025-09-15 14:23:45.123456 +0000 UTC   deployed  nginx-1.21.0    1.21.0
prometheus      monitoring      12        2025-10-01 08:15:33.987654 +0000 UTC   deployed  prometheus-15.0 2.37.0
grafana         monitoring      8         2025-10-01 08:20:11.456789 +0000 UTC   deployed  grafana-6.29.0  9.0.0
```

**Summary:** `3 resources (table format)` ✅

---

### 10. Error Scenarios

#### Connection Timeout
**Raw Output:**
```
Error: connection timeout to API server after 30s
```
**Summary:** `Error: connection timeout to API server after 30s` ✅

#### Authentication Failed
**Raw Output:**
```
Error: failed to authenticate with cluster: invalid credentials
```
**Summary:** `Error: failed to authenticate with cluster: invalid credentials` ✅

#### Resource Not Found
**Raw Output:**
```
Error: pods "non-existent-pod" not found in namespace "default"
```
**Summary:** `Error: pods "non-existent-pod" not found in namespace "default"` ✅

---

## Summary of Rules

| Output Type | Detection Pattern | Summary Format | Example |
|-------------|------------------|----------------|---------|
| **Table** | Header with "NAME", "NAMESPACE", etc. | `{N} resources (table format)` | `6 resources (table format)` |
| **Items List** | Contains "Items:" or "- name:" | `{N} items found` | `5 items found` |
| **Success** | Contains "successfully" or "SUCCESS" | Show full message (≤200 chars) | `pod "nginx" deleted successfully` |
| **Short** | ≤200 chars | Show entire message | `Operation completed` |
| **Long (multi-line)** | >5 lines | `{first line}... ({N} lines total, use include_full_results=true)` | `apiVersion: v1... (34 lines total)` |
| **Long (single block)** | >200 chars, ≤5 lines | `{first 200 chars}... (use include_full_results=true)` | `This is very long... (use include_full_results=true)` |

---

## Usage in Job Results

When viewing job results with **default settings** (page_size=10, include_full_results=false):

```
Job Results (Page 1 of 15)

Job ID: abc-123-def
State: succeeded

✅ c1-sjc3 - 234ms
   Summary: 6 resources (table format)
✅ c1-am2 - 187ms
   Summary: 9 resources (table format)
✅ c1-sv5 - 298ms
   Summary: 7 resources (table format)
...

Showing 10 of 147 results
More results available. Use cursor: MTU=
```

To see **full details** for a specific page:
```
get_job_results(
  job_id: "abc-123-def",
  page_size: 5,
  include_full_results: true  // ← Shows complete output
)
```

This keeps token usage manageable while still providing quick overview of results! 🚀
