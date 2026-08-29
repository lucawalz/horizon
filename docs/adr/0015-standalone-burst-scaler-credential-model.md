---
status: superseded by 0017
date: 2026-07-04
---

# 0015. Narrow horizon to a standalone burst scaler

Superseded in part by [0017](0017-capacity-lease-controller-over-cli-saga.md): the burst orchestration below was replaced by the lease controller, but the credential model stands.

## Context

The narrowing that runs from [0006](0006-cluster-api-operator-pivot.md) through [0014](0014-narrow-horizon-to-on-demand-pool-scaler.md) left horizon as a worker-pool scaler for an existing cluster, but two ties to the surrounding environment remained.

The first was a preflight backup. Before adding capacity and moving a workload, burst took a Velero backup so a failed migration could be restored. That coupled the burst path to a Velero install in the cluster and to the credentials and object store behind it, and it made every burst wait on a backup even though the migration already recorded what it changed and could undo it.

The second was credential sourcing. horizon read the Hetzner token and the node cloud-init from in-cluster Secrets whose name, keys, and encoding were fixed to bedrock's setup: the token from a hardcoded `hcloud` Secret and the cloud-init from the cluster-autoscaler config Secret, from which it parsed the elastic node group's JSON template, base64-decoded it, and rewrote the pool label to reserved. That made horizon depend on the shape and contents of bedrock's autoscaler secret. A rename or a schema change on the bedrock side would break horizon's provisioning even though the token and the cloud-init are horizon's own inputs, not bedrock's to define. The coupling was not the use of a cluster Secret as such but the assumptions baked into how horizon read it.

## Decision

Remove the burst preflight backup and let rollback rely on the state the migration saves in memory. When a burst fails after moving a workload, horizon rolls the affinity and eviction back from the saved state rather than restoring a Velero backup. The Velero backup, restore, and schedule surface leaves horizon entirely.

Source the Hetzner token and the node cloud-init through a flexible credential source configured by horizon rather than through bedrock-specific hardcoding. The source has four variants: an inline `value`, a `path` to a file on disk, the name of an `env` var to read, or a `secret` reference naming a namespace, a secret, and a key to read from the cluster. The variants resolve in that order and horizon fails fast when all are empty. The value, path, and env variants need no cluster access; the secret variant reads the named key from the named secret and returns it verbatim, a generic single-key read that carries no assumption about the secret's name, schema, or encoding. This keeps horizon distributable while letting a deployment that prefers to hold credentials in the cluster continue to do so. The cloud-init supplied through any variant already carries the `horizon.dev/pool=reserved` node label; horizon validates that the label is present and otherwise passes the template through unchanged, rather than parsing an elastic template and rewriting its label.

## Options considered

- Keep reading credentials from the autoscaler Secret with bedrock's fixed name and elastic JSON schema. The status quo. It ties horizon to the name, key, and schema of a Secret that bedrock owns for a different consumer, so a change there breaks horizon for reasons unrelated to horizon.
- Source credentials from config alone, on disk or in env, and drop every cluster read. This is simple but forces credentials onto disk or into the process environment, which a deployment that keeps its secrets in the cluster cannot follow. Rejected because it trades one rigidity for another and narrows who can run horizon.
- Source credentials through a flexible credential source that covers value, path, env, and a generic single-key cluster secret, chosen. The token and the cloud-init are horizon's inputs, so horizon carries the definition of where to read them, and the cluster read that remains makes no assumption about the secret it points at.

## Consequences

horizon owns its Hetzner credentials. A deployment sets the token and the reserved cloud-init in its config through a value, a file path, an env var, or a cluster secret reference, and horizon no longer depends on bedrock's autoscaler Secret or on any fixed secret name or schema for provisioning. What was removed is the bedrock-specific hardcoding, the fixed autoscaler secret name, the elastic JSON and base64 parsing, and the pool-label rewrite, not the ability to read a secret at all; the secret variant that remains is a generic single-key read that any similar stack can point at its own clean cloud-init or token. The cloud-init must already scope its node label to reserved, which horizon checks so a mis-scoped template fails fast instead of provisioning a node that the burst readiness wait would never see.

Burst no longer takes a preflight backup and no longer depends on Velero. A failed migration is undone from saved state, which recovers the affinity and eviction changes but not arbitrary workload data, so burst assumes the moved workload tolerates a reschedule. This continues the narrowing recorded from [0006](0006-cluster-api-operator-pivot.md) through [0014](0014-narrow-horizon-to-on-demand-pool-scaler.md) and supersedes the parts of [0014](0014-narrow-horizon-to-on-demand-pool-scaler.md) that kept the Velero surface and the in-cluster credential reads.
