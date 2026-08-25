---
status: accepted
date: 2026-08-25
---

# 0032. Identify a lease's migrated workloads by lease UID

## Context

`savedPlacementMarkers` in `internal/k8s/migrate.go` stamps a migrated Deployment or StatefulSet with two markers on its own metadata: the annotation `horizon.dev/pre-burst-placement`, holding the workload's original placement as JSON, and the label `horizon.dev/burst-placement: "true"`. Neither records which lease moved it. Lease identity reached only the live pod spec, as node affinity keyed on `horizon.dev/lease-uid` and a toleration valued with the lease name, and neither the migrate path nor the restore path read either.

This is the same shape of defect ADR 0031 fixed one layer down, where a node carried the category label `horizon.dev/pool=reserved` and no owner, so a second lease could not tell its own capacity apart. A migrated workload carries a category and no owner, so a second lease cannot tell its own workloads apart.

Three consequences followed. `restoreWorkloads` filtered on annotation presence alone, so one lease's teardown restored another lease's workloads while that lease was still Active, and the other lease never re-migrated because its `WorkloadMigrated` condition was already True. `migrateWorkloads` skipped an already-annotated workload but still reported it, so a lease wrote another lease's workloads into its own `status.migratedWorkloads`. And both eviction paths swept every non-DaemonSet pod in the namespace except those matched by a self-rolling workload the call itself touched, so one lease's migrate and restore evicted another lease's running pods.

## Decision

Ownership moves onto the marker. The workload keeps `horizon.dev/burst-placement: "true"` as the category and gains `horizon.dev/lease-uid: <uid>` as the identity, the same key the node side already uses, so one identity concept spans nodes and workloads. The value is the UID rather than the lease name because a name can be reused once a lease ends and a UID cannot, which is the reasoning ADR 0031 already records.

Both verbs then compare owners. Migrate patches and claims a workload with no annotation, claims without patching one it already owns, and neither patches nor claims one owned by another lease. Restore restores a workload this lease owns, restores an annotated workload carrying no owner at all, and leaves another lease's workload alone.

Eviction inverts from a skip list to a target list: each path evicts the pods of the workloads that the call itself patched or restored and that do not roll themselves. That states the actual intent rather than approximating it, and it confines both paths to the lease's own workloads.

`ClassifyMigratability` gains the lease identity and a reason naming a workload held by another lease, so the conflict reaches `status.migrationWarnings` before a move is attempted rather than leaving a lease waiting on a readiness gate it can never satisfy.

The asymmetry between the two verbs is deliberate and is the fail-safe direction for each. Migrate must never claim what it cannot prove it owns, because the cost is a false success. Restore must never abandon a workload no lease can claim, because the cost is a workload pinned to a dead lease's affinity, unrecoverable without hand editing, whereas a redundant restore is at worst an extra rollout.

## Options considered

- Record the owning lease inside the placement annotation's JSON. Rejected. Ownership then cannot be answered by a label selector, and it becomes undecidable when the annotation fails to parse, which is an already-handled error case, forcing a choice between abandoning a workload and restoring one that may belong to someone else.
- Replace the `horizon.dev/burst-placement` value `"true"` with the lease UID. Rejected. It collapses the category and the identity into one label and loses the ability to ask whether a workload is on burst capacity at all, which is the same conflation ADR 0031 rejected on the node side.
- Leave the markers alone and have each lease consult the other leases it overlaps with. Rejected. It treats the symptom at teardown rather than fixing the marker both verbs key on, and it needs a lease lister keyed by workload namespace that does not exist.

## Consequences

Two concurrent leases targeting one namespace stay isolated across the whole workload lifecycle, not only in scheduling. Each lease patches, claims, restores and evicts only the workloads it moved.

The eviction inversion also resolves one defect previously carried by the target-set work, that a bare pod with no controlling owner was evicted and nothing recreated it, because a bare pod now matches no patched or restored workload. The Job-pod readiness jam and the PodDisruptionBudget abort mid-namespace are untouched and remain that work's.

`RestorePlacement` and `ClassifyMigratability` gain a lease identity parameter, matching `Migrate`.
