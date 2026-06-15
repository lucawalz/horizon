---
status: accepted
date: 2026-06-15
---

# 0007. Add on-demand capacity through MachineDeployments in two modes

## Context

A burst needs extra worker capacity, and sometimes a whole extra cluster. Cluster API expresses both through the same primitive: a MachineDeployment is a scalable pool of machines, and a Cluster groups pools under a control plane. With CAPH and cluster-api-k3s as the substrate (see [0006](0006-cluster-api-operator-pivot.md)), horizon can drive capacity by reading and scaling those objects rather than provisioning nodes itself. Two situations differ enough to need separate handling: extending the existing home cluster, whose control plane horizon does not manage, and standing up a fresh, fully managed cluster.

## Decision

One pool engine over MachineDeployments serves both, selected by which target the command points at.

Mode A, on-demand nodes, scales an existing worker MachineDeployment (the `burst-workers` pool in `caph-system`) whose machines join the existing home K3s cluster. That cluster's control plane is externally managed, so Cluster API never sets `status.initialization.controlPlaneInitialized` on the Cluster object, and CAPI gates worker bootstrap on that flag. horizon therefore offers a one-time nudge: `up --nudge` patches the status subresource to latch the flag so workers bootstrap. The nudge is the deliberate exception to GitOps durability. It is a status write, not a spec write, so it cannot live in the git tree, and it resets if the Cluster is recreated. `status` warns when the flag is unset.

Mode B, on-demand clusters, creates a separate CAPI-managed cluster through `cluster create`, which renders a Cluster, a KThreesControlPlane, and a worker MachineDeployment. That cluster owns its control plane, so no nudge applies, and it auto-imports to Rancher.

Workload placement is contract, not mechanism owned by horizon. Nodes are labeled `horizon.dev/pool=<value>` at join time by bedrock's KThreesConfigTemplate. `burst` migrates a workload by rewriting node affinity onto that label, so the contract is the label and bedrock owns applying it. horizon never labels nodes itself.

Durable pools and clusters can be rendered into the bedrock git tree (manifest rendering plus a kustomization write) so Flux reconciles them as GitOps. horizon writes the tree but never commits or pushes it.

## Options considered

- Separate code paths for nodes and clusters. Clearer at each call site, but it duplicates the MachineDeployment read, scale, and wait logic that both modes share.
- One pool engine with mode selected by target, chosen. Mode A scales an existing pool under an external control plane, Mode B renders a managed cluster, and both reuse the same MachineDeployment operations.

## Consequences

Both modes share the read, scale, wait, and migrate logic, and the difference between them collapses to the control-plane question and the nudge. The label contract keeps horizon out of node mutation: it rewrites workload affinity but never touches node labels, which bedrock owns. The cost is the nudge, a non-GitOps status write that must be reapplied after any Cluster recreation, surfaced as a `status` warning so it is not silently forgotten. This record supersedes [0004](0004-velero-workload-migration.md), whose migration described horizon labeling the burst node, and [0005](0005-resumable-phase-state-machine.md), whose in-cluster phase machine and local state tracked the retired provisioning path. The Velero backup still precedes migration, and the affinity rewrite and eviction still place the workload, now against the bedrock-owned pool label. The KThreesConfigTemplate that applies the label is recorded in the [bedrock](https://github.com/lucawalz/bedrock/tree/main/docs/adr) repository.
