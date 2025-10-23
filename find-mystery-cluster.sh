#!/bin/bash

echo "Searching for cluster with node IP 198.18.29.86..."

for file in ~/.mcp/*.yaml; do
    cluster=$(basename "$file" .yaml)

    # Try to get nodes and check for the specific IP
    nodes=$(kubectl --kubeconfig "$file" get nodes -o json 2>/dev/null | grep -o '"address":"198.18.29.[0-9]*"' | head -1)

    if [[ ! -z "$nodes" ]]; then
        echo "Found in cluster: $cluster"
        kubectl --kubeconfig "$file" get nodes -o wide 2>/dev/null | head -3
        break
    fi
done