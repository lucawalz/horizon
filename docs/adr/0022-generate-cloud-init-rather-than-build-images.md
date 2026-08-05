---
status: accepted
date: 2026-08-05
---

# 0022. Generate cloud-init rather than build a node image

## Context

[0021](0021-node-side-dead-mans-switch-on-two-clocks.md) added the sentinels a cloud-init template can use to receive the node token, the controller version, and the max lifetime, substituted into whatever blob `spec.hetzner.cloudInitSecretRef` names. Substitution filled in credentials. It did not fill in the one thing a boot script actually needs to become a node: the control plane URL, the join token, the node labels, and the node taint. That part of the contract was left to whoever authored the Secret by hand, against no template and no generator.

The gap was not theoretical. A cloud-init rendered from the sentinels alone carried the node token, the version, and the max lifetime, and nothing that told a k3s agent where to join or what to join as. No server built from it ever registered as a node, and no adopter had a working example to copy, because none existed. The join step was documented as a requirement and shipped as an exercise.

Two shapes close the gap. Bake a working install into a machine image, selected by `spec.hetzner.image`, so a stock boot already knows how to join. Or generate the join step as text at render time, so the image stays generic and the join configuration is produced the same way every time.

## Decision

Generate the cloud-init. `internal/cloudinit` renders a `#cloud-config` document from typed options, `horizon cloud-init` exposes that renderer on the command line, and the result is what an adopter stores behind `cloudInitSecretRef`. horizon ships no image builder: no Packer template, no snapshot pipeline, no CI job that produces a machine image. `spec.hetzner.image` selects an existing image by id, name, or label; it does not create one, and nothing in this repository does.

The arguments against building, recorded so the decision is not reopened:

- **Distribution explosion.** An image is a moving target across at least two axes already present in the schema and the generator: CPU architecture, resolved per lease from the server type, and Kubernetes flavour, `k3s` today with `kubeadm` designed to follow without touching what already exists. A generator adds a flavour by adding a file. An image adds a flavour by adding a build.
- **Lifecycle mismatch.** A base image is rebuilt on a CVE cadence, weeks to months. A `CapacityLease` runs on a workload cadence, `duration` bounded at 8 hours by the schema. Coupling the two means either rebuilding an image for every lease, which defeats the point of an image, or running an image that goes stale for the entire interval between infrequent rebuilds while leases churn underneath it regardless.
- **Credential expansion.** Building a Hetzner image needs permission to create a snapshot, which is not a permission the running system needs for anything else: the operator token creates, gets, lists, and deletes servers, and the delete-capable node token from [0021](0021-node-side-dead-mans-switch-on-two-clocks.md) only deletes. Both credentials have had deliberate effort spent narrowing their blast radius. Adding image-build permission to either widens it for a capability neither otherwise requires.
- **Prior art declines to build.** Karpenter accepts an AMI family or an AMI selector and never produces one. Cluster Autoscaler drives an existing node group and has no image-build step of its own. SkyPilot provisions on stock cloud images and injects its setup at boot. None of the three systems this project has drawn on for its crash-safety and provisioning model own image production, and the reason is the same in each case: the join step is small enough to inject at boot, so there is nothing an image would buy that boot-time injection does not.

## Options considered

- Bake a golden image with k3s and the horizon agent preinstalled, selected through `spec.hetzner.image`. Rejected for the four reasons above.
- Leave the join step to be authored by hand against the documented sentinels. This was the status quo the moment [0021](0021-node-side-dead-mans-switch-on-two-clocks.md) shipped, and it is the option that produced a rendered blob with no join configuration and a server that never joined. The failure to reproduce the setup from documentation alone is the evidence against it, not a hypothetical.
- Generate the cloud-init from typed inputs at render time, chosen. The image stays generic, the join step is produced the same way every time it is asked for, and a second flavour is additive.

## Consequences

horizon now depends on the image it boots running cloud-init and an init system that consumes a `#cloud-config` document, which any current stock Hetzner image already does. That dependency is weaker than depending on a specific, horizon-built image, and it is why `image: {name: ubuntu-24.04}` and `image: {selector: {...}}` both work unchanged against the same generator output.

The image axis and the join axis are independent by construction. Swapping `spec.hetzner.image` changes what the server boots from without touching the cloud-init, and regenerating the cloud-init with `horizon cloud-init` changes how the server joins without touching the image. Neither requires a rebuild of the other.

Adding a second flavour, `kubeadm` or otherwise, is a new file implementing the `cloudinit.Flavor` interface, not a new image pipeline. The generator, not an image, is now the place a second Kubernetes distribution gets added.
