#!/bin/bash

set -ex

source ./cluster/cluster.sh

OCI_BIN=$(if hash podman 2>/dev/null; then echo podman; elif hash docker 2>/dev/null; then echo docker; fi)
namespace=${NAMESPACE:-kubevirt}

if [[ "$KUBEVIRT_PROVIDER" == external ]]; then
    if [[ ! -v DEV_REGISTRY ]]; then
        echo "Missing DEV_REGISTRY variable"
        exit 1
    fi
    push_registry=$DEV_REGISTRY
    manifest_registry=$DEV_REGISTRY
else
    registry_port=$($OCI_BIN port $KUBEVIRT_PROVIDER-registry 5000 | cut -d":" -f2)
    push_registry=localhost:$registry_port
    manifest_registry=registry:5000
fi

bridge_marker_manifest="./manifests/helm/bridge-marker"

REGISTRY=$push_registry make docker-build
REGISTRY=$push_registry make docker-push

# Ensure device plugin is not installed
helm uninstall bridge-marker --ignore-not-found --wait

# Install the device plugin and wait for all daemonset pods to be scheduled on all nodes (default 5m0s)
helm install bridge-marker $bridge_marker_manifest -n $namespace --wait 
