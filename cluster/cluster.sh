export KUBEVIRT_PROVIDER=${KUBEVIRT_PROVIDER:-'k8s-1.30'}

function cluster::path() {
    echo -n ${CLUSTER_PATH}
}
