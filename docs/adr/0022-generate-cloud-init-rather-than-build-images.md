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

Independence holds only while the generated document names no architecture. The image is resolved per lease against the architecture of the server type, so a `cax` size boots an arm image and a `cx` size boots an x86 one from the same `ProviderConfig`. The document therefore resolves the architecture on the booted server, mapping `uname -m` onto the release archive suffix, rather than carrying a choice made at render time. A blob that named an architecture would silently mismatch every server type of the other one: the agent would still join, because the k3s installer detects the architecture itself, and only the watchdog binary would fail to execute, which is the one failure the whole project exists to prevent.

The document does name a Kubernetes version, and that is the opposite call from the architecture for the opposite reason. Architecture varies per lease, resolved from the server type, so a document that named one would be wrong for every server type of the other architecture. The version varies per cluster and not per lease, and the generator renders offline, with no cluster access to discover what the control plane runs, so it cannot fill the value in and cannot check a supplied one against the cluster either. Left unpinned, the install command takes whatever release is newest on the day a node boots: a burst node booted on 6 August installed `v1.36.3+k3s1` against a control plane on `v1.35.6+k3s1`, putting the kubelet ahead of the apiserver and outside the Kubernetes version skew policy. `--kubernetes-version` is therefore required rather than defaulted, and refused unless it names an exact flavour release, so a mismatch is a render-time error instead of a node that joins and is unsupported.

Pinning the generator does not reach a document the generator already produced. The provider reads whatever `cloudInitSecretRef` points at and never re-renders it, so a Secret written before the pin keeps booting nodes on the newest release, and the fix only lands in a deployment once that Secret is regenerated and applied over the old one. That is the generator's standing cost rather than a one-off migration: the same re-render is what a control plane upgrade needs, because a version that was correct when it was rendered drifts out of skew the moment the apiserver moves past it.

Adding a second flavour, `kubeadm` or otherwise, is a new file implementing the `cloudinit.Flavor` interface, not a new image pipeline. The generator, not an image, is now the place a second Kubernetes distribution gets added.

Declining to build an image is not the same as assuming every image is a stock one, and the first version of the generator conflated the two. It emitted the k3s installer unconditionally, so an image that already carried k3s was told to install it over itself; it wrote the watchdog unit to `/etc/systemd/system`, which is a read-only symlink into the Nix store on a NixOS image, with no option other than suppressing the unit entirely and losing the teardown guarantee with it; and it emitted exactly four k3s configuration keys, so a cluster reached over a VPN had no way to set `flannel-iface` and its pods reached nothing. Each of those left an adopter with `--passthrough`, which is not a substitute: passthrough emits no watchdog, so it trades an image mismatch for the loss of the one guarantee the project exists to provide.

The generator therefore carries three capability flags, `--install-kubernetes`, `--transient-watchdog-unit`, and `--flavor-config`, each describing what the image or the network already provides rather than what horizon should build. They default to the previous behaviour, so no document rendered before them changes. Passthrough remains what it always was, the exit for an adopter who owns the whole cloud-init, and is no longer the answer for an adopter whose image simply differs from a stock one.
