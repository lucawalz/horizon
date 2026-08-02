---
status: superseded by 0018
date: 2026-07-04
---

# 0016. Make horizon a cluster-agnostic tool with a provider seam

## Context

The narrowing from [0006](0006-cluster-api-operator-pivot.md) through [0015](0015-standalone-burst-scaler-credential-model.md) left horizon as a standalone burst scaler, but it still assumed the one cluster it grew up in. Three assumptions remained.

Startup required Cluster API. The setup wizard's detection step listed MachineDeployments to discover pools, and that list returns an error on a cluster without the Cluster API CRDs. The error was fatal, so first-run setup could not complete against an ordinary Kubernetes cluster, even though the dashboard already tolerated absent Cluster API and Flux resources and rendered them as unavailable.

Configuration was seeded with values specific to bedrock. When a field was empty the loader filled it with `caph-system`, `burst`, `hel1`, `cpx22`, and the `caph-image-name` image label. Those are one deployment's names, imposed silently on any other, and the setup wizard never asked for the reserved-node credentials at all, so a freshly initialized config could not provision.

The orchestration core imported the Hetzner package directly. The Hetzner SDK itself was already isolated, but `core` depended on the internal hcloud wrapper's types and on node-label constants it defined, so a second provider could not be added without editing the scale and burst paths.

## Decision

Make horizon run against any Kubernetes cluster reached through a kubeconfig, and put the infrastructure provider behind an interface.

Detection tolerates a cluster without Cluster API: a failed MachineDeployment list yields an empty detection rather than an error, and Cluster API and Flux become optional insight layers that light up only where their CRDs exist. The wizard completes with whatever the cluster exposes.

Configuration carries only cluster-agnostic defaults. The bedrock-specific namespace, cluster, location, server type, and image label are removed; a required provider field left empty fails fast with a clear message rather than being filled with a foreign default. The setup wizard collects the reserved-node inputs it used to assume: the token and cloud-init as credential sources per [0015](0015-standalone-burst-scaler-credential-model.md), and the location, server type, image, and SSH keys.

The core depends on a small `provider` interface with two methods, list reserved servers and scale the reserved count to a target, plus a neutral server type and horizon's own node-label constants. The Hetzner package implements that interface with its server spec bound in at construction, and it is the single Hetzner construction site, injected once at the command boundary. The core no longer imports the Hetzner package. A second provider is a new implementation of the same interface selected at that one boundary, with no change to the scale or burst orchestration.

The retired GitOps path field is removed. It was carried since [0012](0012-retire-scaling-thresholds-and-rename-repo-path.md) but nothing consumed it once cluster creation left horizon, so it is deleted along with its wizard field and validation.

## Options considered

- Keep detection fatal and document that horizon needs a Cluster API cluster. Rejected: it contradicts the standalone goal, and the dashboard already degrades gracefully, so only the wizard needed to.
- Abstract every cluster read behind a plugin system so the tool is platform-neutral in full. Rejected as speculative: the reads already tolerate absence, and a plugin framework is weight the tool does not need. The seam is drawn only where a real second implementation would attach, the reserved-node provider.
- Draw the provider interface around the whole Hetzner surface, including image and SSH-key lookup. Rejected: those are reached only through scaling, so the interface stays two methods and the provider-internal detail stays internal.

## Consequences

horizon runs against an ordinary Kubernetes cluster with a kubeconfig and no Cluster API or Flux, and lights up the extra panels where those exist. A new deployment supplies its own names and reserved-node inputs through the wizard and config and is never handed another deployment's defaults; a missing required field fails fast instead of provisioning against a wrong value. A second infrastructure provider is added by implementing the two-method interface and selecting it at the command boundary, without touching the scale, burst, or migration code. The GitOps path field and its wizard step are gone, which supersedes the rename recorded in [0012](0012-retire-scaling-thresholds-and-rename-repo-path.md). What did not change is the Bubble Tea interface and the isolation of the Hetzner SDK; this decision moves horizon's own coupling out of the core, it does not rework the surface a user sees.
