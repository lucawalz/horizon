## Breaking changes

`horizon cloud-init` for the k3s flavour now requires `--kubernetes-version`, unless the image already ships Kubernetes (`--install-kubernetes=false`) or `--passthrough` is used. The generator previously installed whatever k3s version `get.k3s.io` served at boot time, which let a node install a different minor version than its control plane. That version skew caused an incident where a node installed k3s v1.36.3 against a v1.35.6 control plane.

Anyone regenerating cloud-init must pass `--kubernetes-version`. Any cloud-init blob rendered before this release must be regenerated, because fixing the generator does not change documents that were already rendered.
