#!/bin/bash

echo "Searching for cluster with node 'kmaster01'..."

for file in ~/.mcp/*.yaml; do
    cluster=$(basename "$file" .yaml)
    nodes=$(kubectl --kubeconfig "$file" get nodes -o custom-columns=NAME:.metadata.name --no-headers 2>/dev/null | head -1)
    if [[ "$nodes" == "kmaster01" ]]; then
        echo "Found: $cluster has node $nodes"
        kubectl --kubeconfig "$file" get nodes -o wide | head -3
    fi
done