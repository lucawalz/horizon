---
status: accepted
date: 2026-08-25
---

# 0034. Target a set of namespaces rather than one

## Context

`spec.workload` named one namespace as a required string, and the whole migration path threaded that string as a scalar through `Migrate`, `RestorePlacement`, `ClassifyMigratability`, `WorkloadOnBurstNodes`, `WorkloadOffBurstNodes` and the workload clients underneath them. A lease could move one namespace and nothing else, so an operator with a job that spans two namespaces needed two leases, two sets of machines and no way to state that the two moves belong together.

The scalar also hid three defects that only a set makes visible.

A workload reference in `status.migratedWorkloads` and `status.migrationWarnings` was built as `kind/name`. `MigrationWarning` is declared `+listType=map +listMapKey=workload`, so two namespaces each holding a `deployment/api` produce duplicate keys and the apiserver rejects the entire status update. That is not a reporting nicety: every status write from the classification step fails. `status.migratedWorkloads` is `+listType=atomic` and degrades instead to a silent reporting defect, but both are built by the same helper.

Eviction accumulated the selectors of the workloads that do not roll themselves into one flat list and matched it against pod labels alone, with no namespace attached. Across a set that would let a common label such as `app=web` in one namespace govern eviction in another, silently and with nothing logged.

Eviction was reached only when the pass that ran it had patched something, and it returned on the first eviction error. A PodDisruptionBudget that refuses one pod therefore left the rest of its namespace untouched, and on every retry each workload already carried the placement annotation, so nothing was patched and the eviction call was never reached again. A half-migrated namespace was permanent rather than transient.

## Decision

`spec.workload.namespace` is replaced by `spec.workload.namespaces`, a required list of at least one DNS-1123 name declared `+listType=set`, plus an optional `spec.workload.selector`, a standard label selector matched against the workload objects inside those namespaces rather than against the namespaces themselves. An absent selector names every workload, which is the behaviour the scalar had.

The scalar is replaced rather than supplemented. The API is `v1alpha1` and carries no compatibility promise, no `CapacityLease` is committed to Git in either repository, and keeping both fields would mean two ways to express one idea plus a validation rule to referee them.

A workload reference becomes `namespace/kind/name`, so it is unique across a target set. No schema change follows, because the list-map key is a string either way and only its value format changes.

The scalar becomes a validated `TargetSet` at the boundary of `internal/k8s`, built by `NewTargetSet` or, where no selector applies, by `NewNamespaceSet`. Both refuse an empty list, a name that is not a namespace name, and a selector the apiserver cannot compile, so every path downstream receives a set that has already been checked once.

Each namespace is processed independently, end to end. Selectors are collected per namespace and used for eviction in that namespace alone, never merged into one list.

Migration reports a per-namespace outcome. `MigrationResult` carries every workload that moved alongside the namespaces that migrated in full, so a lease that moved two of three namespaces holds `WorkloadMigrated` False with the reason `PartialMigration` and still lists what it moved. Teardown keys the restore on that list rather than on the condition, so workloads moved by a migration that never reached True are still put back.

Both readiness gates fold their per-namespace answers with a conjunction. `WorkloadOffBurstNodes` reads an empty namespace as ready, so any shortcut that stopped at the first ready namespace would let an empty one mask a namespace still holding capacity. `WorkloadOnBurstNodes` additionally requires that at least one pod across the whole set is placed, which keeps its previous meaning while letting an empty namespace in a set not hold the gate open forever.

Eviction is gated on whether any pod of a claimed workload is not yet on one of the lease's nodes, rather than on whether the current pass patched something. It attempts every pod before retrying any, and retries the refused ones on a budget the lease already declares as its teardown grace, with the delay between attempts shrinking with the budget so a short grace still gets more than one attempt. That gives migration the retry semantics the teardown drain already has through `EvictErrorRetryDelay` and `Force`.

The selector applies to migration and classification only. Restore and both gates read every workload in the target namespaces, because restore keys on the placement annotation and the owner label, and a workload whose labels changed while it sat on burst capacity must still be put back.

## Options considered

- Keep `namespace` and add `namespaces` beside it. Rejected. Two fields expressing one idea need a rule deciding which wins, and every reader has to handle both shapes for the life of the API.
- Match the selector against namespaces rather than workloads. Rejected. Namespace labels are usually owned by whoever provisions the namespace, not by whoever runs the workload, so a lease could not narrow itself to the workloads it actually wants without asking for a namespace relabel.
- Apply the selector on the restore path as well, for symmetry. Rejected. A workload whose labels changed while it was on burst capacity would then never be restored, leaving it pinned to a lease that no longer exists, which is the failure ADR 0032 exists to prevent.
- Keep the workload reference unqualified and rely on `status.migratedWorkloads` alone. Rejected. The duplicate key breaks the status write outright rather than merely reading ambiguously, so no amount of care elsewhere makes a second namespace expressible.
- Let a failing namespace fail the whole migration. Rejected. It discards the progress the other namespaces made and gives an operator no way to tell a total failure from a partial one, while teardown still has to put back what did move.
- Requeue rather than retry a refused eviction in place. Rejected as the sole mechanism, because the two eviction paths would then disagree about what a disruption budget costs. The reachability fix means a requeue does retry now; the in-pass budget is what makes a transient refusal clear without waiting for one.

## Consequences

A lease states one target set, so a job that spans namespaces is one lease over one set of machines rather than several leases that have to be coordinated by hand.

`v1alpha1` breaks. Every `CapacityLease` naming `spec.workload.namespace` is refused by the apiserver until it is rewritten, and the terminal leases in the cluster are deleted as part of the rollout. The README, `docs/usage.md` and the web interface are updated with it.

A workload reference reads `namespace/kind/name` in `status.migratedWorkloads` and `status.migrationWarnings`, which changes what those fields look like for a single-namespace lease as well.

The web interface carries a list of namespace entries rather than one, each keeping the autocomplete that the optional namespace-list grant provides, and the lease detail names the selector alongside the namespaces so a narrowed set does not read as the whole of them.

An eviction that a disruption budget refuses now blocks the reconcile for as long as the teardown grace allows rather than returning at once. The delay between attempts scales with the budget, so a lease that declares a short grace still gets more than one attempt rather than spending its whole allowance on a single wait.

A pod that has already finished is no longer evicted by either path, because it holds no capacity to move and would otherwise be evicted again on every pass that finds it stranded.

RBAC is unchanged. The controller's ClusterRole already grants deployments, statefulsets, pods and pods/eviction cluster-wide through a ClusterRoleBinding, so a set of namespaces needs no new rule.
