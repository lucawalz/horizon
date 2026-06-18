---
status: accepted
date: 2026-06-18
---

# 0013. What the Cluster API move bought over Terraform

## Context

horizon began by driving a Terraform module to provision Hetzner nodes ([0001](0001-drive-bedrock-terraform.md)), with cloud calls behind a provider interface meant to make a second backend an implementation detail ([0002](0002-pluggable-provider-interface.md)). The operator pivot ([0006](0006-cluster-api-operator-pivot.md)) removed both and made horizon a thin client over Cluster API objects, and [0010](0010-provider-agnostic-cluster-create-via-clusterclass.md) made `cluster create` render a ClusterClass topology rather than provider-shaped manifests, describing the result as provider-agnostic.

Adding AWS surfaced a fair question. A second cloud still needs its own infrastructure definitions, a Hetzner cluster template or an AWS EKS definition, exactly as a second cloud would need its own Terraform module. If per-provider configuration does not go away, what did the move to Cluster API actually buy over Terraform, and in what sense is the result provider-agnostic? This record answers that honestly, so the claim in 0010 is not read as more than it is.

## Decision

State the distinction plainly rather than leave "provider-agnostic" to imply more than it delivers.

The move to Cluster API did not remove per-provider configuration. Clouds differ, and every tool that provisions them, Terraform, Pulumi, Crossplane, or Cluster API, requires provider-specific definitions. The Cluster API provider for a cloud makes the same API calls the equivalent Terraform provider would. At the layer where infrastructure is described, Cluster API is no more agnostic than Terraform.

What the move made uniform is the layer above provisioning. The `Cluster`, `MachineDeployment`, `Machine`, and `MachinePool` objects are the same across providers, and controllers reconcile them continuously rather than at a point in time. That uniform, self-healing control plane is what horizon operates on, which is why its scale, burst, drain, and status paths work against any similarly shaped provider without change. The agnosticism 0010 claims is real but scoped to the lifecycle and operations layer, not the configuration layer: horizon writes no provider or bootstrap kinds, while the variable keys and values it passes through, a machine type or a region, stay provider-specific.

## Options considered

- Keep the Terraform module and the provider interface ([0001](0001-drive-bedrock-terraform.md), [0002](0002-pluggable-provider-interface.md)). Simplest to reason about and the natural fit for one-shot provisioning. It reconciles only when applied, carries state to store and lock, offers no live remediation or rolling-update semantics, and kept provisioning logic and cloud credentials inside horizon. Set aside at the operator pivot because the cluster does the same work declaratively and continuously.
- Crossplane or Pulumi over the cloud APIs. Also declarative and reconciling, but oriented around generic cloud resources rather than a cluster and machine lifecycle, and still per-provider at the configuration layer. Not adopted, because the platform is already a Cluster API fleet under Rancher Turtles and the machine lifecycle model fits it directly.
- Cluster API, the path taken. A uniform machine and cluster API, continuous reconciliation, self-healing and rolling upgrades as primitives, GitOps-native custom resources with no state file, and horizon reduced to a thin operator over uniform objects. The cost is a heavier substrate, a management cluster, the operator, and a provider per cloud, and the honest scope above: the uniformity is operational, not configurational.

## Consequences

The benefit worth stating out loud is a uniform, continuously reconciling control plane, not write-once-run-anywhere. Describing it as the latter overstates it, and 0010's "provider-agnostic by construction" should be read as true at the template and lifecycle layer only.

The operational uniformity has limits even within Cluster API. AWS EKS, adopted in bedrock's record on running AWS through the managed control plane (bedrock 0036), exposes its workers as a managed node group, a `MachinePool`, rather than the `MachineDeployment`s horizon scales, so horizon does not drive the EKS cluster at all; it is managed declaratively through GitOps instead. The uniform-operations payoff holds for similarly shaped providers and falls away where a provider's shape differs.

For a platform whose purpose is fleet management, self-healing, and a GitOps-native substrate, the move is justified on those properties. For plain provisioning, Terraform would have been simpler, and that is the honest comparison: this choice buys reconciliation and uniformity at the price of a larger substrate.

This record clarifies rather than reverses [0006](0006-cluster-api-operator-pivot.md) and [0010](0010-provider-agnostic-cluster-create-via-clusterclass.md); both stand. It builds on [0001](0001-drive-bedrock-terraform.md) and [0002](0002-pluggable-provider-interface.md) as the path not taken.
