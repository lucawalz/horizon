---
status: accepted
date: 2026-07-04
---

# 0015. Narrow horizon to a standalone burst scaler

## Context

The narrowing that runs from [0006](0006-cluster-api-operator-pivot.md) through [0014](0014-narrow-horizon-to-on-demand-pool-scaler.md) left horizon as a worker-pool scaler for an existing cluster, but two ties to the surrounding environment remained.

The first was a preflight backup. Before adding capacity and moving a workload, burst took a Velero backup so a failed migration could be restored. That coupled the burst path to a Velero install in the cluster and to the credentials and object store behind it, and it made every burst wait on a backup even though the migration already recorded what it changed and could undo it.

The second was credential sourcing. horizon read the Hetzner token and the node cloud-init from in-cluster Secrets: the token from the `hcloud` Secret and the cloud-init from the cluster-autoscaler config Secret, from which it parsed the elastic node group's template and rewrote the pool label to reserved. That made horizon depend on the shape and contents of bedrock's autoscaler secret. A rename or a schema change on the bedrock side would break horizon's provisioning even though the token and the cloud-init are horizon's own inputs, not bedrock's to define.

## Decision

Remove the burst preflight backup and let rollback rely on the state the migration saves in memory. When a burst fails after moving a workload, horizon rolls the affinity and eviction back from the saved state rather than restoring a Velero backup. The Velero backup, restore, and schedule surface leaves horizon entirely.

Source the Hetzner token and the node cloud-init from horizon's own config rather than from cluster Secrets. Both are expressed as a credential source with three variants: an inline `value`, a `path` to a file on disk, or the name of an `env` var to read. horizon resolves the token and the cloud-init through this source and no longer reads any cluster Secret for credentials. The cloud-init supplied through config already carries the `horizon.dev/pool=reserved` node label; horizon validates that the label is present and otherwise passes the template through unchanged, rather than parsing an elastic template and rewriting its label.

## Options considered

- Keep reading credentials from the autoscaler Secret. The status quo. It ties horizon to the name, key, and JSON schema of a Secret that bedrock owns for a different consumer, so a change there breaks horizon for reasons unrelated to horizon.
- Add a config source but keep an in-cluster fallback. This keeps the coupling alive as a second code path and a second failure mode, and it hides which source is authoritative. Rejected in favour of a single source of truth.
- Source credentials from config only, chosen. The token and the cloud-init are horizon's inputs, so horizon carries them, and it reads nothing from the cluster to provision.

## Consequences

horizon owns its Hetzner credentials. A deployment sets the token and the reserved cloud-init in its config through a value, a file path, or an env var, and horizon no longer depends on bedrock's autoscaler Secret or on any cluster Secret for provisioning. The cloud-init must already scope its node label to reserved, which horizon checks so a mis-scoped template fails fast instead of provisioning a node that the burst readiness wait would never see.

Burst no longer takes a preflight backup and no longer depends on Velero. A failed migration is undone from saved state, which recovers the affinity and eviction changes but not arbitrary workload data, so burst assumes the moved workload tolerates a reschedule. This continues the narrowing recorded from [0006](0006-cluster-api-operator-pivot.md) through [0014](0014-narrow-horizon-to-on-demand-pool-scaler.md) and supersedes the parts of [0014](0014-narrow-horizon-to-on-demand-pool-scaler.md) that kept the Velero surface and the in-cluster credential reads.
