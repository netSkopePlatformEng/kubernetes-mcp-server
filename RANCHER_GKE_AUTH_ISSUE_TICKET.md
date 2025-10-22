# SYS Ticket: Rancher Proxy Authentication Failure for Imported GKE Clusters

## Issue Summary
Users with valid Rancher permissions (including Cluster Owner role) are unable to access imported GKE clusters through the Rancher Kubernetes proxy endpoint (`/k8s/clusters/`), receiving 401 Unauthorized errors. The same users can successfully access the Rancher management API and on-premise clusters.

## Affected User
- **Username**: admin (user-7nxzk)
- **Principal**: okta_user://jdambly@netskope.com
- **Groups with Cluster Access**:
  - prod-ncd-k8s-svc-grn (Cluster Owner)
  - npe-k8sadmin-grn (Cluster Owner)
  - prod-eng-all-grn (ns-read-only)
  - prod-ncd-pe-all-grn (ns-read-only)
  - prod-pe-all-grn (ns-read-only)
  - prod-k8s-customersupport-user-grn (ns-read-only)

## Symptoms
1. **401 Unauthorized errors** when accessing GKE clusters via:
   - kubectl with Rancher-generated kubeconfig
   - Direct API calls to `https://rancher.prime.iad0.netskope.com/k8s/clusters/{cluster-id}/`
   - MCP server cluster operations

2. **Working operations**:
   - ✅ Rancher Management API (`/v3/`) - Full access
   - ✅ On-premise clusters (e.g., c6-iad0) - Normal access
   - ✅ Local Rancher cluster - Normal access
   - ✅ Generating new kubeconfig via API - Works but tokens still fail

3. **Failed operations**:
   - ❌ All 21 imported GKE clusters via Kubernetes proxy
   - ❌ Both kubeconfig tokens and API tokens fail equally

## Affected Clusters (Sample)
- gke-cspmv2-2-dev-us-west1 (c-6d6x6)
- gke-rbi-dev-us-west1 (c-nbw7m)
- gke-cfs-development-dev-us-west1
- gke-data-pipeline-stg-us-east1-b
- (All 21 imported GKE clusters show same behavior)

## Technical Evidence

### 1. Token Validation Results
```bash
# API Token test results
curl -H "Authorization: Bearer token-64l2k:*" https://rancher.prime.iad0.netskope.com/v3/clusters/c-6d6x6
# Result: 200 OK ✅

curl -H "Authorization: Bearer token-64l2k:*" https://rancher.prime.iad0.netskope.com/k8s/clusters/c-6d6x6/version
# Result: 401 Unauthorized ❌

# Same token works for on-premise cluster
curl -H "Authorization: Bearer token-64l2k:*" https://rancher.prime.iad0.netskope.com/k8s/clusters/c-dkhgl/version
# Result: 200 OK ✅
```

### 2. Kubeconfig Token Formats Tested
- `kubeconfig-user-7nxzkb8kjp:*` - Generated via UI download
- `kubeconfig-user-7nxzk7wfk8:*` - Generated via API
- `token-64l2k:*` - API token
All show identical behavior: work for v3 API, fail for GKE k8s proxy

### 3. User Permissions Verified
- Global Role: **admin**
- Cluster Membership: **Confirmed via UI and API**
- Group Memberships: **94 Okta groups including required cluster access groups**

### 4. GKE Cluster Configuration
```json
{
  "driver": "GKE",
  "state": "active",
  "imported": true,
  "googleCredentialSecret": "cattle-global-data:cc-xp2w2",
  "projectID": "ns-cspmv2-dev",
  "zone": "us-west1-a"
}
```

## Root Cause Analysis
The issue appears to be with the Rancher-to-GKE authentication proxy mechanism for imported clusters. When Rancher attempts to proxy requests to GKE:

1. **Step 1**: Validate Rancher token ✅ (Works - can access v3 API)
2. **Step 2**: Exchange for GKE credentials ❌ (Fails - returns 401)
3. **Step 3**: Forward to GKE API (Never reached)

The failure at Step 2 suggests the GCP service account credentials stored in `cattle-global-data:cc-xp2w2` may be:
- Expired or rotated
- Lacking necessary IAM permissions
- Not properly configured for the imported clusters

## Impact
- **Severity**: High
- **Affected Users**: All users attempting to access imported GKE clusters through Rancher
- **Business Impact**: Unable to manage GKE clusters through Rancher interface or automation tools

## Recommended Actions

### Immediate Remediation
1. **Verify GCP Service Account**:
   - Check if the service account referenced in `cattle-global-data:cc-xp2w2` exists
   - Verify it has appropriate IAM roles (Kubernetes Engine Admin or similar)
   - Check if credentials have been rotated recently

2. **Test Direct Authentication**:
   - Extract and test the GCP credentials directly
   - Verify they can authenticate to GKE clusters

3. **Review Rancher Logs**:
   - Check Rancher pod logs for GKE authentication errors
   - Look for errors from gke-config-operator pods

### Long-term Solution
1. **Re-import GKE Clusters**:
   - Generate fresh GCP service account credentials
   - Update the secret in Rancher
   - Consider re-importing affected clusters

2. **Implement Monitoring**:
   - Set up alerts for GKE proxy authentication failures
   - Monitor service account credential expiration

## Workarounds for Users
1. **Use Rancher UI**: Access clusters through the web interface if kubectl shell works there
2. **Direct GKE Access**: Use gcloud CLI to get credentials directly:
   ```bash
   gcloud container clusters get-credentials [CLUSTER_NAME] --zone [ZONE] --project [PROJECT]
   ```

## Additional Notes
- Issue affects ONLY imported GKE clusters
- On-premise clusters with direct API access work normally
- Problem is not related to user permissions or RBAC configuration
- All authentication tokens (UI-generated, API-generated) exhibit same behavior

## Contact
- **Reporter**: Jeff Dambly (jdambly@netskope.com)
- **Date**: October 22, 2025
- **Rancher Version**: v2.6.14
- **Affected Environment**: Production (rancher.prime.iad0.netskope.com)

---
*This ticket contains comprehensive debugging information collected via API investigation and should be escalated to the platform/infrastructure team managing Rancher-GKE integration.*