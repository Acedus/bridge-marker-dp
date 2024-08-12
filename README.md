# bridge-marker-dp
Device plugin compatible implementation for [Kubevirt's Bridge Marker](https://github.com/kubevirt/bridge-marker).

# Why?

The original bridge-marker project relies on Kubernetes' built in ability to [advertise extended resources](https://kubernetes.io/docs/tasks/administer-cluster/extended-resource-node/), it does so by directly patching the `node/status` subresource and doesn't check for device health (Kubelet is completely unaware of the extended resource).

The device plugin implementation aims to address several of the original's issue:
* Remove API permissions completely - As a device plugin, bridge-marker only requires access to the host network and Kubelet's device plugins directory, Kubelet takes care of updating the node status instead.
* Allow for proper health monitoring - The previous iteration of bridge-marker doesn't check whether the bridge is "healthy" (e.g. up) and therefore doesn't update the allocatable section of `node/status` for the device which may allow for improper scheduling of consuming Pods.
See manifests/helm for deployment.

# How?

bridge-marker-dp follows the basic implementation of a [Kubernetes device plugin](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/) but it is unique in the sense that bridges aren't considered proper devices. The only implemented part (and the required one) is `ListAndWatch` to serve Kubelet with device updates. Allocation setup is taken care of by bridge CNI.

bridge-marker-dp leverages https://github.com/vishvananda/netlink to subscribe for link updates and act upon them to accurately measure bridge health as well as allow for adding new bridge device plugins at runtime.

# Deploy?

To build and deploy locally using Kubevirt's cluster-up, make sure you have the following installed binaries:
* helm
* kubectl

And run:
```bash
make cluster-sync
```

To build locally:
```bash
make build
```
