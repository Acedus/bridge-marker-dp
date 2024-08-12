# bridge-marker-dp
Device Plugin compatible implementation for Kubevirt's Bridge Marker

See manifests/helm for deployment.

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
